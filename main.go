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
	"strconv"
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

func constructMappings() Mappings{
	return Mappings{
		Workspaces: []WorkspaceMapping{
			{Wid:99999,Description:"Main Workspace"},
		},
		Projects: []ProjectMapping{
			{ProjectId:999, Pid:12345678,Description:"Test Project/Task"},
		},
	}
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

// simple function to post a string to slack api app
func slackBot(url string, s string) {
	fmt.Println("Posting to Slack API using Webhook URL: ", url)
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
		panic("Slack ERROR: " + string(resp.StatusCode) + ", " + string(body))
	} else if string(body) == "ok" {
		fmt.Println("Report sent successfully.")
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <api_token> [slack_webhook_url]\n", os.Args[0])
		fmt.Println("\t api_token \t\t\t - (reqquired) Toggl API Token")
		fmt.Println("\t slack_webhook_url \t - (optional) Webhook URL to Slack API Application")
		return
	}
	// begin configuration
	// TODO: Make this a configuration object (type struct)
	var debug = true; debug = false
	var useSlack = false
	var slack_url = ""
	var username string = os.Args[1]
	var passwd string = "api_token"
	var api_url string = "https://www.toggl.com/api/v8"
	var api_endpoint string = "/time_entries"
	var date_const string = "T00:00:00+02:00"
	var date_from string = fmt.Sprintf("%s%s", time.Now().Format("2006-01-02"), date_const)
	//var date_from string = fmt.Sprintf("%s%s", "2018-01-23", date_const) // for testing, enter custom date
	var m = constructMappings()
	var pe = make(map[int32]*ProjectEntry)
	var total = HoursForPeriod{}
	// end configuration
	if len(os.Args) == 3 { useSlack = true; slack_url = os.Args[2]}
	// TODO: split app logic to functions
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
		if debug { if err != nil { fmt.Printf( "\tERROR matching entry: %s\n", err.Error()); continue } }
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
			pe[p.ProjectId].Description += " " + p.Description
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
	if debug { fmt.Printf("\nAccumulated total time for the period: %dh %dm %ds (%.2fh)\n", total.TotalHours, total.TotalMinutes, total.TotalSeconds, total.TotalFloat) }
	// ############# REPORT #############
	if debug { fmt.Println("Generating report...") }
	var report = ""
	report += fmt.Sprintf(".%-12s+%-11s+%-8s+%-82s,\n", strings.Repeat("-", 12), strings.Repeat("-", 11), strings.Repeat("-", 8), strings.Repeat("-", 82))
	report += fmt.Sprintf("| %-10s | %-9s | %-6s | %-80s |\n", "Date", "Project", "Hours", "Description")
	report += fmt.Sprintf("|%-12s|%-11s|%-8s|%-82s|\n", strings.Repeat("-", 12), strings.Repeat("-", 11), strings.Repeat("-", 8), strings.Repeat("-", 82))
	for k, v := range pe {
		report += fmt.Sprintf("| %-10s | %-9d | %-6.1f | %-80s |\n", v.Date.Format("2006-01-02"), k, v.Total, v.Description)
	}
	report += fmt.Sprintf("`%-12s+%-11s+%-8s+%-82s´\n", strings.Repeat("-", 12), strings.Repeat("-", 11), strings.Repeat("-", 8), strings.Repeat("-", 82))
	if useSlack { slackBot(slack_url, report) } else { fmt.Print(report) }
	/*
	// For debugging print the project map:
	b, _ := json.MarshalIndent(pe, "", "  ")
	fmt.Printf("Project table in JSON format:\n %s", string(b))
	*/
}
