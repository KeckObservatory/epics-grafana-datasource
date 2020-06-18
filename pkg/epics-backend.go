package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
	"io/ioutil"
	"net/http"
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

	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := ds.query(ctx, q)

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

func (ds *EPICSDatasource) query(ctx context.Context, query backend.DataQuery) backend.DataResponse {
	// Unmarshal the json into our queryModel
	var qm queryModel

	response := backend.DataResponse{}

	response.Error = json.Unmarshal(query.JSON, &qm)
	if response.Error != nil {
		return response
	}

	// Log a warning if `Format` is empty.
	if qm.Format == "" {
		log.DefaultLogger.Warn("format is empty. defaulting to time series")
	}

	// create data frame response
	frame := data.NewFrame("response")

	// add the time dimension
	frame.Fields = append(frame.Fields,
		data.NewField("time", nil, []time.Time{query.TimeRange.From, query.TimeRange.To}),
	)

	// add values
	frame.Fields = append(frame.Fields,
		data.NewField("values", nil, []int64{10, 20}),
	)

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

		/*

		   // The only parameter expected to come in is the one indicating for which system to filter on when returning channels
		   service := strings.Split(req.URL.RawQuery, "=")[1]

		   // TODO - Get the channels list, either cached or w/e


		   // Prepare a container to send back to the caller
		   channels := map[string]string{}

		   // Iterate the service list and add to the return array
		   var channel string
		   for rows.Next() {
		     err = rows.Scan(&keyword)
		     if err != nil {
		       log.DefaultLogger.Error(fl() + "keywords scan error")
		       writeResult(rw, "?", nil, err)
		     }

		     // Make a key-value pair for Grafana to use, the key is the bare channel name and the display value (could be different later)
		     channels[channel] = channel
		   }


		   writeResult(rw, "channels", channels, err)

		*/

	} else if strings.HasPrefix(req.URL.String(), "/systems") {
		// Create a systems list based on the list of channels

		// Get the channels as a test of the archiver connection
		var channels []string
		var message string
		channels, err, message = ds.GetArchiverChannels(config.Server, config.ManagePort)

		if err != nil {
			log.DefaultLogger.Error(fl() + "systems retrieve error: " + message)
			writeResult(rw, "?", nil, err)
		}

		// Prepare a container to send back to the caller
		systems := map[string]string{}

		// Iterate the service list and add to the return array
		var system string
		var i int

		//for i = 0; i < len(channels); i++ {
		for i = 0; i < 10; i++ {
			system = channels[i]
			// Make a key-value pair for Grafana to use but the key and the value end up being the same (is this lazy?)
			systems[system] = system

		}

		writeResult(rw, "systems", systems, err)

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
