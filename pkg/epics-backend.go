package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"io/ioutil"
	"math"
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
	UNIT_CONVERT_F_TO_C        = iota
	UNIT_CONVERT_C_TO_F        = iota
)

// Define the data transforms, this maps onto the transformOptions list in QueryEditor.tsx
const (
	TRANSFORM_NONE                  = iota
	TRANSFORM_FIRST_DERIVATVE       = iota
	TRANSFORM_FIRST_DERIVATVE_1HZ   = iota
	TRANSFORM_FIRST_DERIVATVE_10HZ  = iota
	TRANSFORM_FIRST_DERIVATVE_100HZ = iota
	TRANSFORM_DELTA                 = iota
	TRANSFORM_TRUNCATE_FRAC_SECS    = iota
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
		res := ds.query(ctx, q, config.Server, config.ManagePort, config.DataPort)

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
	DisableBinning bool   `json:"disablebinning"`
	IntervalMs     int    `json:"intervalMs"`
	MaxDataPoints  int    `json:"maxDataPoints"`
	OrgId          int    `json:"orgId"`
	RefId          string `json:"refId"`
	Hide           bool   `json:"hide"`
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
		PREC float64 `json:"PREC,string"`
		Name string  `json:"name"`
	} `json:"meta"`
}

type PVStringData []struct {
	Data []struct {
		Nanos    int64  `json:"nanos"`
		Secs     int64  `json:"secs"`
		Severity int64  `json:"severity"`
		Status   int64  `json:"status"`
		Val      string `json:"val"`
	} `json:"data"`
	Meta struct {
		PREC float64 `json:"PREC,string"`
		Name string  `json:"name"`
	} `json:"meta"`
}

func (ds *EPICSDatasource) query(ctx context.Context, query backend.DataQuery, server string, manageport string, dataport string) backend.DataResponse {

	// Unmarshal the json into our queryModel
	var qm queryModel

	response := backend.DataResponse{}

	// Return an error if the unmarshal fails
	response.Error = json.Unmarshal(query.JSON, &qm)
	if response.Error != nil {
		return response
	}

	// Return nothing if we are hiding this channel
	if qm.Hide {
		return response
	}

	// Create an empty data frame response and add time dimension
	empty_frame := data.NewFrame("response")
	empty_frame.Fields = append(empty_frame.Fields, data.NewField("time", nil, []time.Time{query.TimeRange.From, query.TimeRange.To}))

	// Return empty frame query is empty
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

	// The EPICS Archiver can bin the data for us into slices of 1 or more seconds.
	// https://slacmshankar.github.io/epicsarchiver_docs/userguide.html
	//
	// If the number of seconds in the query is less than the max amount of data Grafana wants back, we have
	// to bin it ourselves with a smaller interval than 1 second.

	// How long is the requested time range?
	querylength := query.TimeRange.To.Sub(query.TimeRange.From).Seconds()

	// If the number of seconds in the query is larger than the max requested data points, we can calculate 1 second
	// bins by using ratio of the query time to the max data points.
	binsize := math.Floor(querylength / float64(query.MaxDataPoints))

	var sampleRate int64

	// Do our own binning if we have to, for now just return the raw data and let the browser deal with it
	if qm.DisableBinning {
		sampleRate = 0
	} else if binsize < 1 {
		// TODO - This is where we will bin it ourselves
		sampleRate = 0
	} else {
		// Else tell the archiver to do it for us
		sampleRate = int64(binsize)
	}

	log.DefaultLogger.Debug(fmt.Sprintf("querylength = %f  sampleRate = %d  MaxDataPoints = %d", querylength, sampleRate, query.MaxDataPoints))

	// URL encode the params
	params := url.Values{}
	params.Add("from", query.TimeRange.From.Format(time.RFC3339Nano))
	params.Add("to", query.TimeRange.To.Format(time.RFC3339Nano))

	if sampleRate > 0 {
		params.Add("pv", fmt.Sprintf("lastSample_%d(%s)", sampleRate, channel))
	} else {
		// Retrieve the data, unbinned
		params.Add("pv", channel)
	}

	// Generate a URL to query for the channel data
	getdataurl := fmt.Sprintf("http://%s:%s/retrieval/data/getData.json?%s", server, dataport, params.Encode())
	log.DefaultLogger.Debug(fmt.Sprintf("Archiver URL = %s", getdataurl))

	// Give the archiver 1 minute to reply
	client := http.Client{Timeout: time.Second * 60}

	httpreq, err := http.NewRequest(http.MethodGet, getdataurl, nil)
	if err != nil {
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
	var pvsdata PVStringData

	// 2020-06-24 PMR: Look for NaN values which will cause the unmarshal below to fail.  Replace with nulls.
	body = bytes.Replace(body, []byte(": NaN"), []byte(": null"), -1)

	// Try to unmarshal as floats, this is the vast majority of cases
	if err := json.Unmarshal(body, &pvdata); err != nil {

		// If that didn't work, unmarshal as strings
		if _, ok := err.(*json.UnmarshalTypeError); ok {

			err := json.Unmarshal(body, &pvsdata)
			if err != nil {
				// Send back an empty frame, couldn't make strings out of it, either
				response.Frames = append(response.Frames, empty_frame)
				response.Error = err
				return response
			}

			// If it worked as strings, we can't do much other than just ship them back up as-is, no transforms
			// Determine how many strings came back
			var count int
			for _, pvsdataset := range pvsdata {
				count += len(pvsdataset.Data)
			}

			// Store times and values here before building the response
			times := make([]time.Time, count)
			values := make([]string, count)

			var i int
			for _, pvsdataset := range pvsdata {
				for _, pvsdatarow := range pvsdataset.Data {

					// Assign to the frame
					values[i] = pvsdatarow.Val

					// One of the transforms is to remove the fractional seconds from the timestamps.  Used when computing
					// differences between two channels.  This will force the timestamps to line up.  Only works well with
					// 1Hz data or slower.
					if qm.Transform == TRANSFORM_TRUNCATE_FRAC_SECS {
						times[i] = time.Unix(int64(pvsdatarow.Secs), 0)
					} else {
						times[i] = time.Unix(int64(pvsdatarow.Secs), int64(pvsdatarow.Nanos))
					}
					i++
				}
			}

			// Start a new frame and add the times + values
			frame := data.NewFrame("response")
			frame.RefID = qm.RefId
			frame.Name = qm.QueryText
			frame.Fields = append(frame.Fields, data.NewField("time", nil, times))
			frame.Fields = append(frame.Fields, data.NewField("values", nil, values))

			// add the frames to the response
			response.Frames = append(response.Frames, frame)
			return response

		} else {
			// Send back an empty frame, it wasn't a unmarshal type error
			response.Frames = append(response.Frames, empty_frame)
			response.Error = err
			return response
		}
	}

	// Determine how many points came back
	var count int
	for _, pvdataset := range pvdata {
		count += len(pvdataset.Data)
	}

	log.DefaultLogger.Debug(fmt.Sprintf("Returning %d data points", count))

	// Store times and values here before building the response
	times := make([]time.Time, count)
	values := make([]float64, count)

	// Temporary variables for conversions/transforms
	var val float64
	var i int

	for _, pvdataset := range pvdata {
		for _, pvdatarow := range pvdataset.Data {

			// If we are doing a unit conversion, perform it now while we have the single value in hand
			switch qm.UnitConversion {

			case UNIT_CONVERT_NONE:
				// No conversion, just assign it straight over
				val = pvdatarow.Val

			case UNIT_CONVERT_DEG_TO_RAD:
				// RAD = DEG * π/180  (1° = 0.01745rad)
				val = pvdatarow.Val * (math.Pi / 180)

			case UNIT_CONVERT_RAD_TO_DEG:
				// DEG = RAD * 180/π  (1rad = 57.296°)
				val = pvdatarow.Val * (180 / math.Pi)

			case UNIT_CONVERT_RAD_TO_ARCSEC:
				// ARCSEC = RAD * (3600 * 180)/π  (1rad = 206264.806")
				val = pvdatarow.Val * (3600 * 180 / math.Pi)

			case UNIT_CONVERT_K_TO_C:
				// °C = K + 273.15
				val = pvdatarow.Val + 273.15

			case UNIT_CONVERT_C_TO_K:
				// K = °C − 273.15
				val = pvdatarow.Val - 273.15

			case UNIT_CONVERT_F_TO_C:
				// °C = (°F − 32) × 5⁄9
				val = (pvdatarow.Val - 32) * 5 / 9

			case UNIT_CONVERT_C_TO_F:
				// °F = (°C * 9/5) + 32
				val = (pvdatarow.Val * 9 / 5) + 32

			default:
				// Send back an empty frame with an error, we did not understand the conversion
				response.Frames = append(response.Frames, empty_frame)
				response.Error = fmt.Errorf("Unknown unit conversion: %d", qm.UnitConversion)
				return response
			}

			// Assign to the frame
			values[i] = val

			// One of the transforms is to remove the fractional seconds from the timestamps.  Used when computing
			// differences between two channels.  This will force the timestamps to line up.  Only works well with
			// 1Hz data or slower.
			if qm.Transform == TRANSFORM_TRUNCATE_FRAC_SECS {
				times[i] = time.Unix(int64(pvdatarow.Secs), 0)
			} else {
				times[i] = time.Unix(int64(pvdatarow.Secs), int64(pvdatarow.Nanos))
			}

			i++
		}
	}

	// Perform any requested data transforms
	switch qm.Transform {

	case TRANSFORM_NONE:
		break

	case TRANSFORM_FIRST_DERIVATVE, TRANSFORM_FIRST_DERIVATVE_1HZ, TRANSFORM_FIRST_DERIVATVE_10HZ, TRANSFORM_FIRST_DERIVATVE_100HZ:

		// Compute the first derivative of the data.
		dtimes := make([]time.Time, count-1)
		dvalues := make([]float64, count-1)

		for i = 1; i < count; i++ {
			// Calculate the dt
			dtimes[i-1] = times[i]

			// Calculate the dy/dt
			var dt, dvdt float64
			dt = (times[i].Sub(times[i-1])).Seconds()
			dvdt = (values[i] - values[i-1]) / dt

			if qm.Transform == TRANSFORM_FIRST_DERIVATVE_1HZ {
				dvdt = math.Round(dvdt)
			} else if qm.Transform == TRANSFORM_FIRST_DERIVATVE_10HZ {
				dvdt = math.Round(dvdt*10) / 10
			} else if qm.Transform == TRANSFORM_FIRST_DERIVATVE_100HZ {
				dvdt = math.Round(dvdt*100) / 100
			}

			dvalues[i-1] = dvdt
		}

		// Reassign the original arrays to be the 1st derivative results
		times = dtimes
		values = dvalues

	case TRANSFORM_DELTA:
		// Compute the deltas of the data.  This algorithm replicates what numpy diff() does in Python,
		// to the extent that it disregards the time series data.  The resultant arrays have one fewer element,
		// we drop the 0th element of time and value.  It's like a first derivative where dt is always 1.
		// See https://numpy.org/doc/stable/reference/generated/numpy.diff.html
		dtimes := make([]time.Time, count-1)
		dvalues := make([]float64, count-1)

		for i = 1; i < count; i++ {
			// Bring the time val straight across, shifted by one
			dtimes[i-1] = times[i]

			// Calculate the dx/dt and assume dt is always 1
			dvalues[i-1] = values[i] - values[i-1]
		}

		// Reassign the original arrays to be the new results
		times = dtimes
		values = dvalues

	case TRANSFORM_TRUNCATE_FRAC_SECS:
		// Nothing to do here, this would have been handled up above when the nanoseconds were dropped in the time creation
		break

	default:
		// Send back an empty frame with an error, we did not understand the transform
		response.Frames = append(response.Frames, empty_frame)
		response.Error = fmt.Errorf("Unknown transform: %d", qm.Transform)
		return response
	}

	// Start a new frame and add the times + values
	frame := data.NewFrame("response")
	frame.RefID = qm.RefId
	frame.Name = qm.QueryText

	// It looks like you can submit the values with any string for a name, which will be appended to the
	// .Name field above (thus creating a series named "service.KEYWORD values" which may not be the desired
	// name for the series.  Thus, submit it with an empty string for now which appears to work.
	//frame.Fields = append(frame.Fields, data.NewField("values", nil, values))
	frame.Fields = append(frame.Fields, data.NewField("Value", nil, values))
	frame.Fields = append(frame.Fields, data.NewField("Time", nil, times))

	// add the frames to the response
	response.Frames = append(response.Frames, frame)

	return response

}

type GetPVStatus []struct {
	Appliance                  string  `json:"appliance"`
	ConnectionFirstEstablished string  `json:"connectionFirstEstablished"`
	ConnectionLastRestablished string  `json:"connectionLastRestablished"`
	ConnectionLossRegainCount  int64   `json:"connectionLossRegainCount,string"`
	ConnectionState            bool    `json:"connectionState,string"`
	IsMonitored                bool    `json:"isMonitored,string"`
	LastEvent                  string  `json:"lastEvent"`
	LastRotateLogs             string  `json:"lastRotateLogs"`
	PvName                     string  `json:"pvName"`
	PvNameOnly                 string  `json:"pvNameOnly"`
	SamplingPeriod             float64 `json:"samplingPeriod,string"`
	Status                     string  `json:"status"`
}

func (ds *EPICSDatasource) GetArchiverChannels(Server string, ManagePort string, SingleChannel string) ([]string, []float64, error, string) {

	// Init a container for the raw pv list
	pvs := make(GetPVStatus, 0)

	// Generate a URL to query for the channel list, or a single channel if that's what is required
	var getpvurl string
	if SingleChannel != "" {

		// URL encode the params for the single channel
		params := url.Values{}
		params.Add("pv", SingleChannel)

		getpvurl = fmt.Sprintf("http://%s:%s/mgmt/bpl/getPVStatus?%s", Server, ManagePort, params.Encode())

	} else {
		getpvurl = fmt.Sprintf("http://%s:%s/mgmt/bpl/getPVStatus", Server, ManagePort)
	}

	// Give the archiver 30 seconds to reply
	client := http.Client{Timeout: time.Second * 30}

	httpreq, err := http.NewRequest(http.MethodGet, getpvurl, nil)
	if err != nil {
		return []string{}, []float64{}, err, "Failure to create HTTP request: " + err.Error()
	}

	// Retrieve the PV list
	res, err := client.Do(httpreq)
	if err != nil {
		return []string{}, []float64{}, err, "Failure to GET archiver PV names: " + err.Error()
	}

	// Pull the body out of the response
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return []string{}, []float64{}, err, "Failure to read body from archiver GET request: " + err.Error()
	}

	// Decode the body into component PVs
	err = json.Unmarshal(body, &pvs)
	if err != nil {
		return []string{}, []float64{}, err, "Failure to unmarshal PV list JSON: " + err.Error()
	}

	// Init containers for the channel list and sampling periods
	channels := make([]string, len(pvs))
	periods := make([]float64, len(pvs))
	var i int
	for i = 0; i < len(pvs); i++ {
		channels[i] = pvs[i].PvName
		periods[i] = pvs[i].SamplingPeriod
	}

	// Return the completed PV list
	return channels, periods, nil, ""
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
	channels, _, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort, "")

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
		allchannels, _, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort, "")

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
		channels, _, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort, "")

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
