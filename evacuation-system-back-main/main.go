package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/tools/hook"
)

const CISCO_VALIDATOR_KEY string = "909415abfb1ab86854b5b6a94d8bb529d8091b02"
const CISCO_URL string = "/api/cisco"

type FloorPlan struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	X    interface{} `json:"x"`
	Y    interface{} `json:"y"`
}

type RSSIRecord struct {
	ApMac string `json:"apMac"`
	RSSI  int    `json:"rssi"`
}

type Location struct {
	Lng          interface{}  `json:"lng"`
	RSSIRecords  []RSSIRecord `json:"rssiRecords"`
	Variance     interface{}  `json:"variance"`
	FloorPlan    FloorPlan    `json:"floorPlan"`
	NearestApMac string       `json:"nearestApMac"`
	Time         string       `json:"time"`
	Lat          interface{}  `json:"lat"`
}

type BleBeacon struct {
	UUID    string `json:"uuid"`
	TxPower int    `json:"txPower"`
	Major   int    `json:"major"`
	BleType string `json:"bleType"`
	Minor   int    `json:"minor"`
}

type LatestRecord struct {
	Time          string `json:"time"`
	NearestApMac  string `json:"nearestApMac"`
	NearestApRssi int    `json:"nearestApRssi"`
}

type Observation struct {
	Locations    []Location   `json:"locations"`
	Name         string       `json:"name"`
	BleBeacons   []BleBeacon  `json:"bleBeacons"`
	ClientMac    string       `json:"clientMac"`
	LatestRecord LatestRecord `json:"latestRecord"`
}

type ReportingAP struct {
	Serial    string      `json:"serial"`
	Mac       string      `json:"mac"`
	Name      string      `json:"name"`
	Lng       interface{} `json:"lng"`
	Tags      []string    `json:"tags"`
	FloorPlan FloorPlan   `json:"floorPlan"`
	Lat       interface{} `json:"lat"`
}

type Data struct {
	NetworkID    string        `json:"networkId"`
	StartTime    string        `json:"startTime"`
	ReportingAps []ReportingAP `json:"reportingAps"`
	EndTime      string        `json:"endTime"`
	Observations []Observation `json:"observations"`
}

type Payload struct {
	Version string `json:"version"`
	Type    string `json:"type"`
	Secret  string `json:"secret"`
	Data    Data   `json:"data"`
}

type ScannedBeacon struct {
	MacAddress string  `json:"mac_address"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Worksite   string  `json:"worksite"`
	Beacon     string  `json:"beacon"`
	ScannedAt  string  `json:"scanned_at"`
}

func GetLatLongAsFloat64(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		fmt.Println("Value (Float):", v)
		return v
	case string:
		floatVal, err := strconv.ParseFloat(v, 64)
		if err != nil {
			fmt.Println("Error:", err)
			return 0
		}
		fmt.Println("Value (String):", floatVal)
		return floatVal

	default:

		fmt.Println("Unsupported data type")
		return 0
	}
}

type BeaconUsers struct {
	ID          string `db:"id" json:"id"`
	MacAddress  string `db:"mac_address" json:"mac_address"`
	AssignedTo  string `db:"assigned_to" json:"assigned_to"`
	Name        string `db:"name" json:"name"`
	Role        string `db:"role" json:"role"`
	Company     string `db:"company" json:"company"`
	ArrivalTime string `db:"arrival" json:"arrival_time"`
	Message     string `json:"message"`
	Created     string `db:"created" json:"created"`
}

func main() {

	app := pocketbase.NewWithConfig(&pocketbase.Config{})

	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPost,
			Path:   "/api/evacuation-beacons",
			Handler: func(c echo.Context) error {
				body, err := io.ReadAll(c.Request().Body)

				if err != nil {
					return apis.NewApiError(500, "failed to read request body", err)
				}
				beaconUsers := make([]BeaconUsers, 0)
				jsonPayload, _ := json.Marshal(beaconUsers)
				err = json.Unmarshal(body, &beaconUsers)

				if err != nil {
					return apis.NewBadRequestError(err.Error(), string(jsonPayload))
				}
				data := make([]BeaconUsers, 0)
				if len(beaconUsers) > 0 {

					for _, obs := range beaconUsers {

						record, _ := app.Dao().FindRecordById("evacuation_beacons", obs.ID)
						if record != nil {
							record.Set("safepoint_arrival_date", time.Now().UTC())
							if obs.ArrivalTime != "" {
								record.Set("safepoint_arrival_date", obs.ArrivalTime)
							}

							if err := app.Dao().SaveRecord(record); err != nil {
								fmt.Println(err, record)
								data = append(data, obs)
							}

						} else {
							obs.Message = "data not found"
							data = append(data, obs)
						}

					}
				}

				return c.JSON(http.StatusOK, data)

			},
			Middlewares: []echo.MiddlewareFunc{
				apis.ActivityLogger(app),
			},
		})

		return nil
	})

	//Get Info from people on the evacuation
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/people-on-evacuation/:evacuationId",
			Handler: func(c echo.Context) error {

				evacuationId := c.PathParam("evacuationId")
				if evacuationId == "0" {

					type Result struct {
						ID          string `db:"id" json:"id"`
						LastCreated string `db:"last_created" json:"last_created"`
					}
					result := Result{}
					err := app.DB().NewQuery("SELECT id, max(created) as last_created from evacuations").One(&result)
					if err != nil {
						return apis.NewApiError(500, "Internal Server error", err)
					}
					evacuationId = result.ID
				}

				query := app.DB().NewQuery("Select eb.id, b.mac_address, b.assigned_to, (p.firstname || ' ' ||p.lastname) AS name, coalesce(eb.safepoint_arrival_date,'') AS arrival, (t.name|| '-' ||p.type ) as role, c.name as company,eb.created  FROM beacons b INNER JOIN evacuation_beacons eb ON eb.beacon=b.id INNER JOIN people p ON b.assigned_to= p.id INNER JOIN companies c ON c.id= p.company INNER JOIN teams t ON t.id= p.team where eb.evacuation={:evacuationId}")
				query.Bind(dbx.Params{"evacuationId": evacuationId})

				beaconsUsers := make([]BeaconUsers, 0)

				er := query.All(&beaconsUsers)
				if er != nil {
					return apis.NewApiError(500, "Internal Server error", er)
				}
				return c.JSON(http.StatusOK, beaconsUsers)
			},
			Middlewares: []echo.MiddlewareFunc{
				apis.ActivityLogger(app),
			},
		})
		return nil
	})

	// Cisco meraki validator
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   CISCO_URL,
			Handler: func(c echo.Context) error {
				return c.String(200, CISCO_VALIDATOR_KEY)
			},
			Middlewares: []echo.MiddlewareFunc{
				apis.ActivityLogger(app),
			},
		})

		return nil
	})

	// Cisco meraki receiver
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPost,
			Path:   CISCO_URL,
			Handler: func(c echo.Context) error {
				// Read the request body
				body, err := io.ReadAll(c.Request().Body)
				body = bytes.Replace(body, []byte("NaN"), []byte("0"), -1)
				if err != nil {
					return apis.NewApiError(500, "failed to read request body", err)
				}

				// Parse the request body into the Data struct
				var payload Payload
				jsonPayload, _ := json.Marshal(payload)
				err = json.Unmarshal(body, &payload)
				if err != nil {
					return apis.NewBadRequestError(err.Error(), string(jsonPayload))
				}

				if payload.Data.NetworkID == "" {
					return apis.NewBadRequestError("no networkID specified", string(jsonPayload))
				}

				worksite, err := app.Dao().FindFirstRecordByData("worksites", "external_id", payload.Data.NetworkID)

				if err != nil {
					return apis.NewApiError(500, "failed to find worksite", err)

				}

				if worksite == nil {
					return apis.NewBadRequestError("unknown network", err)
				}

				// Process the data...
				// Convert observations to ScannedBeacon array
				scannedBeacons := make([]ScannedBeacon, 0)
				for _, obs := range payload.Data.Observations {
					var latestLocation *Location // Initialize latestLocation as nil

					if len(obs.Locations) > 0 {
						latestLocation = &obs.Locations[len(obs.Locations)-1] // Get the latest location
					}

					scannedBeacon := ScannedBeacon{
						MacAddress: obs.ClientMac,
						Worksite:   worksite.Id,
						ScannedAt:  payload.Data.EndTime,
					}

					if latestLocation != nil {

						latitude := GetLatLongAsFloat64(latestLocation.Lat)
						longitude := GetLatLongAsFloat64(latestLocation.Lng)

						scannedBeacon.Latitude = latitude
						scannedBeacon.Longitude = longitude
					}

					scannedBeacons = append(scannedBeacons, scannedBeacon)
				}

				scannedBeaconsColletion, err := app.Dao().FindCollectionByNameOrId("scanned_beacons")
				if err != nil {
					fmt.Println(err)
					return err
				}
				// Create a channel to receive the results
				resultCh := make(chan ScannedBeacon)

				// Loop through the scannedBeacons array and run async code (database read) for each beacon
				for _, beacon := range scannedBeacons {
					go func(b ScannedBeacon) {

						record := models.NewRecord(scannedBeaconsColletion)
						record.Set("mac_address", b.MacAddress)
						record.Set("worksite", b.Worksite)
						record.Set("scanned_at", b.ScannedAt)

						if b.Latitude != 0 && b.Longitude != 0 {
							record.Set("latitude", b.Latitude)
							record.Set("longitude", b.Longitude)
						}

						beaconRecord, err := app.Dao().FindFirstRecordByData("beacons", "mac_address", b.MacAddress)
						if err != nil {
							fmt.Println(err)
						}

						if beaconRecord != nil {
							record.Set("beacon", beaconRecord.Id)
							b.Beacon = beaconRecord.Id
						}

						if err := app.Dao().SaveRecord(record); err != nil {
							fmt.Println(err)
							return
						}
						// Send the updated ScannedBeacon to the result channel
						resultCh <- b
					}(beacon)
				}

				// Wait for results from all goroutines
				updatedBeacons := make([]ScannedBeacon, 0)
				for range scannedBeacons {
					updatedBeacon := <-resultCh
					updatedBeacons = append(updatedBeacons, updatedBeacon)
				}

				// Close the result channel
				close(resultCh)
				return c.JSON(http.StatusOK, updatedBeacons)
			},
			Middlewares: []echo.MiddlewareFunc{
				apis.ActivityLogger(app),
			},
		})

		return nil
	})

	//
	app.OnRecordAfterCreateRequest("evacuations").Add(func(e *core.RecordCreateEvent) error {
		log.Println(e.Record.Id)
		// Logica para copier beacons
		return nil
	})

	// Purge old records
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/purge-scanned-beacons",
			Handler: func(c echo.Context) error {
				return c.String(200, "will be implemented soon")
			},
			Middlewares: []echo.MiddlewareFunc{
				apis.ActivityLogger(app),
			},
		})

		return nil
	})

	//Save after evacuation starts
	app.OnModelAfterCreate().Add(func(e *core.ModelEvent) error {
		modelRecord, _ := e.Model.(*models.Record)
		if modelRecord.Collection().Name == "evacuations" {
			worksite := modelRecord.Get("worksite")
			evacuation_beacons, _ := app.Dao().FindCollectionByNameOrId("evacuation_beacons")
			recordsView, _ := app.Dao().FindRecordsByExpr("scanned_beacons_summary", dbx.HashExp{"worksite": worksite}, dbx.NewExp("(assigned_to not null AND assigned_to <> '' ) AND (beacon not null or beacon<> '') AND last_seen_date >= datetime('now', '-2 minutes')"))
			for _, obs := range recordsView {
				record := models.NewRecord(evacuation_beacons)
				record.Set("beacon", obs.Get("beacon"))
				record.Set("evacuation", modelRecord.Get("id"))
				record.Set("assigned_to", obs.Get("assigned_to"))
				if err := app.Dao().SaveRecord(record); err != nil {
					fmt.Println(err)
					return nil
				}

			}

		}
		return nil
	})

	app.OnRecordBeforeCreateRequest().Add(func(e *core.RecordCreateEvent) error {

		if e.Record.Collection().Name == "evacuations" {
			worksiteId := e.Record.GetString("worksite")
			inProgress, _ := app.Dao().FindRecordsByExpr("evacuations", dbx.HashExp{"worksite": worksiteId}, dbx.NewExp("end_date IS NULL OR end_date = ''"))

			if len(inProgress) > 0 {
				e.HttpContext.JSON(http.StatusOK, inProgress)
				return hook.StopPropagation
			}

		}

		return nil
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}

}
