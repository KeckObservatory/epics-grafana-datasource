package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"io/ioutil"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// Get the file and line number for logging clarity
func fl() string {
	_, fileName, fileLine, ok := runtime.Caller(1)

	// Strip out the pathing information from the filename
	ss := strings.Split(fileName, "/")
	shortFileName := ss[len(ss)-1]

	var s string
	if ok {
		s = fmt.Sprintf("(%s:%d) ", shortFileName, fileLine)
	} else {
		s = ""
	}
	return s
}

// DatasourceSettings contains archiver connection information
type DatasourceSettings struct {
	Server     string `json:"server"`
	ManagePort string `json:"managePort"`
	DataPort   string `json:"dataPort"`
}

// Define the unit conversions, this maps onto the unitConversionOptions list in QueryEditor.tsx
const (
	UNIT_CONVERT_NONE          = iota
	UNIT_CONVERT_DEG_TO_RAD    = iota
	UNIT_CONVERT_RAD_TO_DEG    = iota
	UNIT_CONVERT_RAD_TO_ARCSEC = iota
	UNIT_CONVERT_K_TO_C        = iota
	UNIT_CONVERT_C_TO_K        = iota
)

// Define the data transforms, this maps onto the transformOptions list in QueryEditor.tsx
const (
	TRANSFORM_NONE                  = iota
	TRANSFORM_FIRST_DERIVATVE       = iota
	TRANSFORM_FIRST_DERIVATVE_1HZ   = iota
	TRANSFORM_FIRST_DERIVATVE_10HZ  = iota
	TRANSFORM_FIRST_DERIVATVE_100HZ = iota
	TRANSFORM_DELTA                 = iota
)

// LoadSettings gets the relevant settings from the plugin context
func LoadSettings(ctx backend.PluginContext) (*DatasourceSettings, error) {
	model := &DatasourceSettings{}

	settings := ctx.DataSourceInstanceSettings
	err := json.Unmarshal(settings.JSONData, &model)
	if err != nil {
		return nil, fmt.Errorf("error reading settings: %s", err.Error())
	}

	return model, nil
}

// newDatasource returns datasource.ServeOpts.
func newDatasource() datasource.ServeOpts {
	// creates a instance manager for your plugin. The function passed
	// into `NewInstanceManger` is called when the instance is created
	// for the first time or when a datasource configuration changed.
	log.DefaultLogger.Info(fl() + "Creating new EPICS datasource")

	im := datasource.NewInstanceManager(newDataSourceInstance)
	ds := &EPICSDatasource{
		im: im,
	}

	mux := http.NewServeMux()
	httpResourceHandler := httpadapter.New(mux)

	// Bind the HTTP paths to functions that respond to them
	mux.HandleFunc("/systems", ds.handleResourceChannels)
	mux.HandleFunc("/channels", ds.handleResourceChannels)

	return datasource.ServeOpts{
		CallResourceHandler: httpResourceHandler,
		QueryDataHandler:    ds,
		CheckHealthHandler:  ds,
	}
}

// EPICSDatasource is an example datasource used to scaffold
// new datasource plugins with an backend.
type EPICSDatasource struct {
	// The instance manager can help with lifecycle management
	// of datasource instances in plugins. It's not a requirements
	// but a best practice that we recommend that you follow.
	im instancemgmt.InstanceManager
}

// QueryData handles multiple queries and returns multiple responses.
// req contains the queries []DataQuery (where each query contains RefID as a unique identifer).
// The QueryDataResponse contains a map of RefID to the response for each query, and each response
// contains Frames ([]*Frame).
func (ds *EPICSDatasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
	log.DefaultLogger.Info("QueryData", "request", req)

	// create response struct
	response := backend.NewQueryDataResponse()

	// Get the configuration
	config, err := LoadSettings(req.PluginContext)
	if err != nil {
		log.DefaultLogger.Error(fl() + "settings load error")
		return nil, err
	}

	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := ds.query(ctx, q, config.Server, config.DataPort)

		// save the response in a hashmap
		// based on with RefID as identifier
		response.Responses[q.RefID] = res
	}

	return response, nil
}

type queryModel struct {
	//Datasource string `json:"datasource"`
	//DatasourceId string `json:"datasourceId"`
	Format         string `json:"format"`
	QueryText      string `json:"queryText"`
	UnitConversion int    `json:"unitConversion"`
	Transform      int    `json:"transform"`
	IntervalMs     int    `json:"intervalMs"`
	MaxDataPoints  int    `json:"maxDataPoints"`
	OrgId          int    `json:"orgId"`
	RefId          string `json:"refId"`
}

// Structure obtained with https://github.com/bashtian/jsonutils
type PVData []struct {
	Data []struct {
		Nanos    int64   `json:"nanos"`
		Secs     int64   `json:"secs"`
		Severity int64   `json:"severity"`
		Status   int64   `json:"status"`
		Val      float64 `json:"val"`
	} `json:"data"`
	Meta struct {
		PREC int64  `json:"PREC,string"`
		Name string `json:"name"`
	} `json:"meta"`
}

func (ds *EPICSDatasource) query(ctx context.Context, query backend.DataQuery, server string, dataport string) backend.DataResponse {

	// Unmarshal the json into our queryModel
	var qm queryModel

	response := backend.DataResponse{}

	// Return an error if the unmarshal fails
	response.Error = json.Unmarshal(query.JSON, &qm)
	if response.Error != nil {
		return response
	}

	// Create an empty data frame response and add time dimension
	empty_frame := data.NewFrame("response")
	empty_frame.Fields = append(empty_frame.Fields, data.NewField("time", nil, []time.Time{query.TimeRange.From, query.TimeRange.To}))

	// Return empty frame if query is empty
	if qm.QueryText == "" {

		// add the frames to the response
		response.Frames = append(response.Frames, empty_frame)
		return response
	}

	// Log a warning if `Format` is empty.
	if qm.Format == "" {
		log.DefaultLogger.Warn("format is empty. defaulting to time series")
	}

	// Channel is the query text
	channel := qm.QueryText

	// Ask the archive server to perform binning based on the length of time requested

	// The EPICS Archiver will bin the data into arbitrary sized groups, taking the first or last
	// sample as the representative value in the bin.  Start to bin the data when the data source
	// request is larger than 30 minutes.
	// https://slacmshankar.github.io/epicsarchiver_docs/userguide.html
	//
	// Use the Grafana panel request and MaxDataPoints to generate appropriate sized bins.
	//
	//  Request Bin size          Param to send
	// <10m     do not bin
	//  30m     5 second bins     lastSample_5
	//   4h     15 second bins    lastSample_15
	//   8h     30 second bins    lastSample_30
	//   1d     2 minute bins     lastSample_120
	//   2d     3 minute bins     lastSample_180
	//   1w     10 minute bins    lastSample_600
	//   2w     20 minute bins    lastSample_1200
	//   1M     1 hour bins       lastSample_3600
	//   6M     4 hour bins       lastSample_14400
	//   1Y>    12 hour bins      lastSample_43200

	// setSamplingOption does the same and interpolates for other time ranges

	// How long is the requested time range?
	ranget := query.TimeRange.To.Sub(query.TimeRange.From).Minutes()

	// Calculate how many minutes are in the above ranges
	const minutes10 = 10.0
	const hoursHalf = 30.0
	const hours4 = 60.0 * 4
	const hours8 = 60.0 * 8
	const hoursDay = 60.0 * 24
	const hours2Days = hoursDay * 2
	const hoursWeek = hoursDay * 7
	const hours2Weeks = hoursWeek * 2
	const hoursMonth = hoursWeek * 4 // Close enough
	const hours6Month = hoursMonth * 6
	const hoursYear = hoursDay * 365

	var sampleRate int

	if ranget <= minutes10 {
		sampleRate = 0
	} else if ranget <= hoursHalf {
		sampleRate = 5
	} else if ranget <= hours4 {
		sampleRate = 15
	} else if ranget <= hours8 {
		sampleRate = 30
	} else if ranget <= hoursDay {
		sampleRate = 120
	} else if ranget <= hours2Days {
		sampleRate = 180
	} else if ranget <= hoursWeek {
		sampleRate = 600
	} else if ranget <= hours2Weeks {
		sampleRate = 1200
	} else if ranget <= hoursMonth {
		sampleRate = 3600
	} else if ranget <= hours6Month {
		sampleRate = 14400
	} else if ranget <= hoursYear {
		sampleRate = 43200
	} else {
		// Else same as 1 year
		sampleRate = 43200
	}

	log.DefaultLogger.Debug(fmt.Sprintf("ranget = %f  sampleRate = %d  MaxDataPoints = %d", ranget, sampleRate, query.MaxDataPoints))

	// URL encode the params
	params := url.Values{}
	params.Add("from", query.TimeRange.From.Format(time.RFC3339))
	params.Add("to", query.TimeRange.To.Format(time.RFC3339))

	if sampleRate > 0 {
		params.Add("pv", fmt.Sprintf("lastSample_%d(%s)", sampleRate, channel))
	} else {
		// Retrieve the data, unbinned
		params.Add("pv", channel)
	}

	// Generate a URL to query for the channel data
	getdataurl := fmt.Sprintf("http://%s:%s/retrieval/data/getData.json?%s", server, dataport, params.Encode())

	// Give the archiver 1 minute to reply
	client := http.Client{Timeout: time.Second * 60}

	httpreq, err := http.NewRequest(http.MethodGet, getdataurl, nil)
	if err != nil {
		// Send back an empty frame, the query failed in some way
		response.Frames = append(response.Frames, empty_frame)
		response.Error = err
		return response
	}

	// Retrieve the channel data
	res, err := client.Do(httpreq)
	if err != nil {
		// Send back an empty frame, the query failed in some way
		response.Frames = append(response.Frames, empty_frame)
		response.Error = err
		return response
	}

	// Pull the body out of the response
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		// Send back an empty frame, the query failed in some way
		response.Frames = append(response.Frames, empty_frame)
		response.Error = err
		return response
	}

	// Init a container for the raw data points
	var pvdata PVData

	// Decode the body
	err = json.Unmarshal(body, &pvdata)
	if err != nil {
		// Send back an empty frame, the query failed in some way
		response.Frames = append(response.Frames, empty_frame)
		response.Error = err
		return response
	}

	// Determine how many points came back
	var count int
	for _, pvdataset := range pvdata {
		count += len(pvdataset.Data)
	}

	// Store times and values here before building the response
	times := make([]time.Time, count)
	values := make([]float64, count)

	// Temporary variables for conversions/transforms
	//var timetemp float64
	//var valtemp, val float64
	var i int32

	for _, pvdataset := range pvdata {
		for _, pvdatarow := range pvdataset.Data {
			values[i] = pvdatarow.Val
			times[i] = time.Unix(int64(pvdatarow.Secs), int64(pvdatarow.Nanos))
			i++
		}
	}

	// Start a new frame and add the times + values
	frame := data.NewFrame("response")
	frame.RefID = qm.RefId
	frame.Name = qm.QueryText

	// It looks like you can submit the values with any string for a name, which will be appended to the
	// .Name field above (thus creating a series named "service.KEYWORD values" which may not be the desired
	// name for the series.  Thus, submit it with an empty string for now which appears to work.
	//frame.Fields = append(frame.Fields, data.NewField("values", nil, values))
	frame.Fields = append(frame.Fields, data.NewField("", nil, values))
	frame.Fields = append(frame.Fields, data.NewField("time", nil, times))

	// add the frames to the response
	response.Frames = append(response.Frames, frame)

	return response

}

type pv struct {
	LastRotateLogs             string `lastRotateLogs:"string"`
	Appliance                  string `appliance:"string"`
	PvName                     string `pvName:"string"`
	PvNameOnly                 string `pvNameOnly:"string"`
	ConnectionState            string `connectionState:"string"`
	LastEvent                  string `lastEvent:"string"`
	SamplingPeriod             string `samplingPeriod:"string"`
	IsMonitored                string `isMonitored:"string"`
	ConnectionLastRestablished string `connectionLastRestablished:"string"`
	ConnectionFirstEstablished string `connectionFirstEstablished:"string"`
	ConnectionLossRegainCount  string `connectionLossRegainCount:"string"`
	Status                     string `status:"string"`
}

func (ds *EPICSDatasource) GetArchiverChannels(Server string, ManagePort string) ([]string, error, string) {

	// Init a container for the raw pv list
	pvs := make([]pv, 0)

	// Generate a URL to query for the channel list
	getpvurl := fmt.Sprintf("http://%s:%s/mgmt/bpl/getPVStatus", Server, ManagePort)

	// Give the archiver 30 seconds to reply
	client := http.Client{Timeout: time.Second * 30}

	httpreq, err := http.NewRequest(http.MethodGet, getpvurl, nil)
	if err != nil {
		return []string{}, err, "Failure to create HTTP request: " + err.Error()
	}

	// Retrieve the PV list
	res, err := client.Do(httpreq)
	if err != nil {
		return []string{}, err, "Failure to GET archiver PV names: " + err.Error()
	}

	// Pull the body out of the response
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return []string{}, err, "Failure to read body from archiver GET request: " + err.Error()
	}

	// Decode the body into component PVs
	err = json.Unmarshal(body, &pvs)
	if err != nil {
		return []string{}, err, "Failure to unmarshal PV list JSON: " + err.Error()
	}

	// Init a container for just the channel list
	channels := make([]string, len(pvs))
	var i int
	for i = 0; i < len(pvs); i++ {
		channels[i] = pvs[i].PvName
	}

	// Return the completed PV list
	return channels, nil, ""
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (ds *EPICSDatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	var status = backend.HealthStatusOk
	var message = "Data source is working"

	config, err := LoadSettings(req.PluginContext)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "Invalid config",
		}, nil
	}

	// Get the channels as a test of the archiver connection
	var channels []string
	channels, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort)

	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "Failure to get channels: " + message,
		}, nil

	} else {
		// Confirmation success back to the user
		message = fmt.Sprintf("Connection confirmed to %s:%s, found %d PVs", config.Server, config.ManagePort, len(channels))
	}

	return &backend.CheckHealthResult{
		Status:  status,
		Message: message,
	}, nil
}

func writeResult(rw http.ResponseWriter, path string, val interface{}, err error) {
	response := make(map[string]interface{})
	code := http.StatusOK
	if err != nil {
		response["error"] = err.Error()
		code = http.StatusBadRequest
	} else {
		response[path] = val
	}

	body, err := json.Marshal(response)
	if err != nil {
		body = []byte(err.Error())
		code = http.StatusInternalServerError
	}
	_, err = rw.Write(body)
	if err != nil {
		code = http.StatusInternalServerError
	}
	rw.WriteHeader(code)
}

func (ds *EPICSDatasource) handleResourceChannels(rw http.ResponseWriter, req *http.Request) {
	log.DefaultLogger.Debug(fl() + "resource call url=" + req.URL.String() + "  method=" + req.Method)

	if req.Method != http.MethodGet {
		return
	}

	// Get the configuration
	ctx := req.Context()
	config, err := LoadSettings(httpadapter.PluginConfigFromContext(ctx))
	if err != nil {
		log.DefaultLogger.Error(fl() + "settings load error")
		writeResult(rw, "?", nil, err)
		return
	}

	// Retrieve the channels for a given system
	if strings.HasPrefix(req.URL.String(), "/channels") {

		// The only parameter expected to come in is the one indicating for which system to filter the channels by
		params, err := url.ParseQuery(req.URL.RawQuery)
		if err != nil {
			log.DefaultLogger.Error(fl() + "channels URL error: " + err.Error())
			writeResult(rw, "?", nil, err)
			return
		}
		system := params.Get("system")

		// Get the channels list fresh from the archiver (again)
		var allchannels []string
		var message string
		allchannels, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort)

		if err != nil {
			log.DefaultLogger.Error(fl() + "channels retrieve error: " + message)
			writeResult(rw, "?", nil, err)
			return
		}

		// Prepare a container to send back to the caller
		channels := map[string]string{}

		// Iterate the allchannels list and filter it down, based on the specified system name
		for i := 0; i < len(allchannels); i++ {

			channel := allchannels[i]

			if strings.Contains(channel, system) {
				channels[channel] = channel
			}
		}

		writeResult(rw, "channels", channels, err)

	} else if strings.HasPrefix(req.URL.String(), "/systems") {
		// Create a systems list based on the list of channels

		// Get the channels list fresh from the archiver
		var channels []string
		var message string
		channels, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort)

		if err != nil {
			log.DefaultLogger.Error(fl() + "systems retrieve error: " + message)
			writeResult(rw, "?", nil, err)
			return
		}

		// Prepare a container to send back to the caller
		systems := map[string]string{}

		// Always provide an empty string for "clearing" the selection
		systems[""] = "(none)"

		// Iterate the channels list and determine what the system prefixes are
		for i := 0; i < len(channels); i++ {

			channel := channels[i]
			segs := strings.Split(channel, ":")

			// Categorize channels with 3 parts (k0:met:primtemp) as the first two segments (k0:met)
			// Categorize channels with 4 parts (k1:dcs:axe:az) as the first three segments (k1:dcs:axe)
			if len(segs) == 3 || len(segs) == 4 {
				system := strings.Join(segs[:len(segs)-1], ":") + ":"
				systems[system] = system
			}
		}

		writeResult(rw, "systems", systems, err)
		return

	} else {

		// If we got this far, it was a bogus request
		log.DefaultLogger.Error(fl() + "invalid request string")
		writeResult(rw, "?", nil, err)
		return
	}
}

type instanceSettings struct {
	httpClient *http.Client
}

func newDataSourceInstance(setting backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	return &instanceSettings{
		httpClient: &http.Client{},
	}, nil
}

func (s *instanceSettings) Dispose() {
	// Called before creatinga a new instance to allow plugin authors
	// to cleanup.
}
