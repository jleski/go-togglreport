package main

import (
"fmt"
"net/http"
"encoding/json"
"io/ioutil"
"net/url"
"errors"
"time"
"strings"
"unicode/utf8"
"os"
"bytes"
	"log"
)

type Hours struct {
	Hours int16
	Minutes int16
	Seconds int16
	Float float32
}

type HoursForPeriod struct {
	TotalDuration int32
	TotalHours int16
	TotalMinutes int16
	TotalSeconds int16
	TotalFloat float32
}

type Entry struct {
	Id int64 `json:"id"`
	Guid string `json:"guid"`
	Wid int32 `json:"wid"`
	Pid int32 `json:"pid"`
	Billable bool `json:"billable"`
	Start string `json:"start"`
	Duration int64 `json:"duration"`
	Description string `json:"description"`
	Duronly bool `json:"duronly"`
	At string `json:"at"`
	Uid int64 `json:"uid"`
	Tags []string `json:"tags"`
}

type ProjectMapping struct {
	ProjectId int32
	Pid int32
	Description string
}

type WorkspaceMapping struct {
	Wid int32
	Description string
}

type Mappings struct {
	Workspaces[] WorkspaceMapping
	Projects[] ProjectMapping
}

type ProjectEntry struct {
	Entries []Entry
	Date time.Time
	Description string
	Hours int16
	Minutes int16
	Seconds int16
	Duration int32
	Total float32
	ProjectId int32
}

type Config struct {
	Mappings `json:"mappings"`
	Debug bool `json:"debug"`
	Api_url string `json:"api_url"`
	Api_endpoint string `json:"api_endpoint"`
	Time_constant string `json:"time_constant"`
	Default_workspace WorkspaceMapping `json:"default_workspace"`
	Default_project ProjectMapping `json:"default_project"`
	Pretty_print bool `json:"pretty_print"`
	Disable_slack bool `json:"disable_slack"`
}

func resolveMapping( m Mappings, e Entry ) (W WorkspaceMapping, P ProjectMapping, err error) {
	for _, w := range m.Workspaces {
		if w.Wid == e.Wid {
			for _, p := range m.Projects { if e.Pid  == p.Pid { return w, p,nil } }
			return WorkspaceMapping{}, ProjectMapping{}, errors.New(fmt.Sprintf("Unable to match project %d", e.Pid))
		}
	}
	return WorkspaceMapping{}, ProjectMapping{}, errors.New(fmt.Sprintf("Unknown workspace %d", e.Wid))
}

func azWrite(file string, str string) {
	// just write the length to the queue location. Just to prove this works.
	cacheFile, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE, 0)
	if err != nil {
		log.Fatalf("Unable to open file %s %s", file, err)
	}
	cacheFile.WriteString( str + "\n")
	cacheFile.Close()
}

// simple function to post a string to slack api app
func slackBot(url string, s string, az string) {
	if az != "" { azWrite( az, fmt.Sprintln("Posting to Slack API using Webhook URL: ", url) )	} else { fmt.Println("Posting to Slack API using Webhook URL: ", url) }
	s = "```" + s + "```"
	var jsonStr = []byte(`{"text":"Dear sir, here's your Toggl Report:\n` + s + `"}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		if az != "" { azWrite(az, fmt.Sprintln("Slack ERROR: " + string(resp.StatusCode) + ", " + string(body))) } else { panic("Slack ERROR: " + string(resp.StatusCode) + ", " + string(body)) }
	} else if string(body) == "ok" {
		if az != "" { azWrite(az, fmt.Sprintln("Report sent successfully.")) } else {	fmt.Println("Report sent successfully.") }
	}
}

func LoadConfiguration(file string) Config {
	var config Config
	configFile, err := os.Open(file)
	defer configFile.Close()
	if err != nil {
		fmt.Println(err.Error())
	}
	jsonParser := json.NewDecoder(configFile)
	jsonParser.Decode(&config)
	return config
}

func main() {
	username := ""
	useSlack := false
	slack_url := ""
	az_output := ""
	if len(os.Args) < 2 {
		if os.Getenv("AZURE_FUNCTIONS_ENV") != "" {
			if os.Getenv("toggl_api_token") != "" { username = os.Getenv("toggl_api_token")}
			if os.Getenv("slack_webhook_url") != "" { useSlack = true; slack_url = os.Getenv("slack_webhook_url")}
		} else {
			fmt.Printf("Usage: %s <api_token> [slack_webhook_url]\n", os.Args[0])
			fmt.Println("\t api_token \t\t\t - (reqquired) Toggl API Token")
			fmt.Println("\t slack_webhook_url \t - (optional) Webhook URL to Slack API Application")
			return
		}
	} else {
		username = os.Args[1]
		if len(os.Args) == 3 { useSlack = true; slack_url = os.Args[2] }
	}

	// ================ begin configuration ================
	config_file := "config.json"
	conf := LoadConfiguration(config_file)
	debug := conf.Debug
	passwd := "api_token" // this is hard-coded by Toggl
	api_url := conf.Api_url
	api_endpoint := conf.Api_endpoint
	date_const := conf.Time_constant
	date_from := fmt.Sprintf("%s%s", time.Now().Format("2006-01-02"), date_const)
	//date_from := fmt.Sprintf("%s%s", "2018-01-31", date_const) // for testing, enter custom date
	if conf.Disable_slack {
		if debug {
			fmt.Println("Disabled Slack integration (config.json)")
		}
		useSlack = false
	}
	var m = conf.Mappings
	var pe = make(map[int32]*ProjectEntry)
	var total = HoursForPeriod{}
	// ================ end configuration ================

	if debug { fmt.Printf("Querying time entries for date: %s\n", date_from) }
	client := &http.Client{}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s%s?start_date=%s", api_url, api_endpoint, url.QueryEscape(date_from)), nil)
	req.SetBasicAuth(username, passwd)
	resp, err := client.Do(req)
	if err != nil{
		panic(err)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	r := make([]Entry, 0)
	err = json.Unmarshal(body, &r)
	if err != nil {
		panic(err)
	}
	if debug { fmt.Printf("Found %d entries:\n", len(r)) }
	for _, obj := range r {
		t, err := time.Parse(time.RFC3339, obj.Start)
		if err != nil {
			fmt.Println("ERROR: " + err.Error())
		}
		if debug { fmt.Printf("%d\t%d\t%s\t%s\n", obj.Wid, obj.Pid, obj.Description, obj.Start) }
		w, p, err := resolveMapping( m, obj )
		if debug {
			if err != nil {
				fmt.Printf( "\tERROR matching entry: %s\n", err.Error())
			}
		}
		if err != nil {
			w = conf.Default_workspace
			p = conf.Default_project
			p.Description = fmt.Sprintf(p.Description, obj.Description)
			w.Description = fmt.Sprintf(w.Description, obj.Wid)
		}
		var h = &Hours{}
		var durationAsInt = int16(0)
		if obj.Duration < 0 {
			d := time.Since(t)
			durationAsInt = int16(d.Seconds())
			if debug { fmt.Printf("\t- Still tracking: %s\n", d) }
		} else  {
			durationAsInt = int16(obj.Duration)
		}
		total.TotalDuration += int32(durationAsInt)
		h.Hours = durationAsInt / 60 / 60
		h.Minutes = (durationAsInt - (h.Hours * 60 * 60)) / 60
		h.Seconds = durationAsInt - (h.Hours * 60 * 60) - (h.Minutes * 60)
		h.Float = float32(h.Hours) + (float32(h.Minutes) / 60) + (float32(h.Seconds / 3600))
		if p.Description[utf8.RuneCountInString(p.Description)-1:] != "." {
			p.Description += "."
		}
		if debug { fmt.Printf("%s (%d), Project %d %s, Hours: %dh %dm %ds (%.2fh)\n", w.Description, w.Wid, p.ProjectId, p.Description, h.Hours, h.Minutes, h.Seconds, h.Float) }
		// store entry in project map
		if _, ok := pe[p.ProjectId]; !ok {
			pe[p.ProjectId] = &ProjectEntry{[]Entry{}, t, strings.TrimSpace(p.Description), h.Hours, h.Minutes, h.Seconds, int32(durationAsInt), float32(h.Float), p.ProjectId}
			pe[p.ProjectId].Entries = append(pe[p.ProjectId].Entries, obj)
		} else {
			pe[p.ProjectId].Entries = append(pe[p.ProjectId].Entries, obj)
			pe[p.ProjectId].Duration += int32(durationAsInt)
			// TODO: This is not unicode-safe, should use strings.ContainsRune() instead
			if !strings.Contains(pe[p.ProjectId].Description, p.Description) {
				pe[p.ProjectId].Description += " " + p.Description
			}
			pe[p.ProjectId].Total += float32(h.Float)
		}
		// sum hours per project
		var projectDurationAsInt = int32(pe[p.ProjectId].Duration)
		pe[p.ProjectId].Hours = int16(projectDurationAsInt) / 60 / 60
		pe[p.ProjectId].Minutes = (int16(projectDurationAsInt) - (pe[p.ProjectId].Hours * 60 * 60)) / 60
		pe[p.ProjectId].Seconds = int16(projectDurationAsInt) - (pe[p.ProjectId].Hours * 60 * 60) - (pe[p.ProjectId].Minutes * 60)
		if debug { fmt.Println("--") }
	}
	// count totals for the period
	var TotalDurationAsInt16 = int16(total.TotalDuration)
	total.TotalHours = TotalDurationAsInt16 / 60 / 60
	total.TotalMinutes = (TotalDurationAsInt16 - (total.TotalHours * 60 * 60)) / 60
	total.TotalSeconds = TotalDurationAsInt16 - (total.TotalHours * 60 * 60) - (total.TotalMinutes * 60)
	total.TotalFloat = float32(total.TotalHours) + (float32(total.TotalMinutes) / 60) + (float32(total.TotalSeconds / 3600))
	total_string := fmt.Sprintf("Accumulated total time for the period: %dh %dm %ds (%.2fh)\n", total.TotalHours, total.TotalMinutes, total.TotalSeconds, total.TotalFloat)
	if debug {  fmt.Printf("%s", total_string)}
	// ############# REPORT #############
	if debug { fmt.Println("Generating report...") }
	var report = ""
	if conf.Pretty_print {
		report += fmt.Sprintf(".%-12s+%-11s+%-8s+%-82s,\n", strings.Repeat("-", 12), strings.Repeat("-", 11), strings.Repeat("-", 8), strings.Repeat("-", 82))
		report += fmt.Sprintf("| %-10s | %-9s | %-6s | %-80s |\n", "Date", "Project", "Hours", "Description")
		report += fmt.Sprintf("|%-12s|%-11s|%-8s|%-82s|\n", strings.Repeat("-", 12), strings.Repeat("-", 11), strings.Repeat("-", 8), strings.Repeat("-", 82))
	}
	for k, v := range pe {
		if conf.Pretty_print {
			report += fmt.Sprintf("| %-10s | %-9d | %-6.1f | %-80s |\n", v.Date.Format("2006-01-02"), k, v.Total, v.Description)
		} else {
			report += fmt.Sprintf("Date: %s\nProject: %d\nHours: %.1f\nDescription: %s\n\n", v.Date.Format("2006-01-02"), k, v.Total, v.Description)
		}
	}
	if conf.Pretty_print {
		report += fmt.Sprintf("`%-12s+%-11s+%-8s+%-82s´\n", strings.Repeat("-", 12), strings.Repeat("-", 11), strings.Repeat("-", 8), strings.Repeat("-", 82))
	}
	report += fmt.Sprintf("%s", total_string)
	if useSlack { slackBot(slack_url, report, az_output) } else { fmt.Print(report) }
	/*
	// For debugging print the project map:
	b, _ := json.MarshalIndent(pe, "", "  ")
	fmt.Printf("Project table in JSON format:\n %s", string(b))
	*/
}