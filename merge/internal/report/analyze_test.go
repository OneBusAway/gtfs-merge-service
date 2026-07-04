package report

import (
	"reflect"
	"strings"
	"testing"
)

// baseFixture is a small, valid GTFS feed: 2 agencies, 3 stops, 2 routes,
// 2 trips, 1 calendar row, plus a calendar_dates.txt row introducing a
// service_id calendar.txt doesn't have.
func baseFixture() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name\n" +
			"1,Everett Transit\n" +
			"2,Sound Transit\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"1001,Main St,47.90,-122.20\n" +
			"1002,Broadway,47.91,-122.21\n" +
			"1003,Pine St,47.92,-122.22\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_type\n" +
			"100,1,1,3\n" +
			"101,1,2,3\n",
		"trips.txt": "route_id,service_id,trip_id\n" +
			"100,WK,T1\n" +
			"101,WK,T2\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"WK,1,1,1,1,1,0,0,20260101,20261231\n",
		"calendar_dates.txt": "service_id,date,exception_type\n" +
			"WK,20260704,2\n" +
			"HOL,20261225,1\n",
	}
}

func TestAnalyzeZipCounts(t *testing.T) {
	zipPath := newGTFSZip(t, baseFixture())

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}

	wantCounts := Counts{Stops: 3, Routes: 2, Trips: 2, Calendars: 2} // 1 from calendar.txt + 1 (HOL) only in calendar_dates.txt
	if a.Counts != wantCounts {
		t.Errorf("Counts = %+v, want %+v", a.Counts, wantCounts)
	}
}

func TestAnalyzeZipServiceRangeFromCalendar(t *testing.T) {
	zipPath := newGTFSZip(t, baseFixture())

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}

	want := ServiceRange{Start: "20260101", End: "20261231"}
	if a.ServiceRange != want {
		t.Errorf("ServiceRange = %+v, want %+v", a.ServiceRange, want)
	}
}

func TestAnalyzeZipServiceRangeFallsBackToCalendarDates(t *testing.T) {
	files := baseFixture()
	delete(files, "calendar.txt")
	// Without calendar.txt, every calendar_dates.txt service_id is "new".
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}

	wantRange := ServiceRange{Start: "20260704", End: "20261225"}
	if a.ServiceRange != wantRange {
		t.Errorf("ServiceRange = %+v, want %+v", a.ServiceRange, wantRange)
	}
	// calendar.txt is gone, so calendars = both calendar_dates.txt service_ids (WK, HOL).
	if a.Counts.Calendars != 2 {
		t.Errorf("Counts.Calendars = %d, want 2", a.Counts.Calendars)
	}
}

func TestAnalyzeZipBBoxSkipsBoundaryAndBlankRows(t *testing.T) {
	files := baseFixture()
	files["stops.txt"] = "stop_id,stop_name,stop_lat,stop_lon,location_type\n" +
		"1001,Main St,47.90,-122.20,0\n" +
		"1002,Broadway,47.91,-122.21,0\n" +
		"1003,Pine St,,,0\n" + // blank lat/lon: skipped
		"9001,Boundary Area,10.0,10.0,4\n" // location_type 4 (boundary): skipped
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}
	if a.BBox == nil {
		t.Fatalf("BBox is nil, want a populated bbox")
	}
	want := BBox{MinLat: 47.90, MaxLat: 47.91, MinLon: -122.21, MaxLon: -122.20}
	if *a.BBox != want {
		t.Errorf("BBox = %+v, want %+v", *a.BBox, want)
	}
	// The boundary/blank rows are still counted as stops.
	if a.Counts.Stops != 4 {
		t.Errorf("Counts.Stops = %d, want 4", a.Counts.Stops)
	}
}

func TestAnalyzeZipBBoxIncludesAllRowsWithoutLocationType(t *testing.T) {
	files := baseFixture()
	// No location_type column at all: every row with valid coordinates
	// counts toward the bbox, per the milestone's "otherwise include all
	// stops with coords" rule.
	files["stops.txt"] = "stop_id,stop_name,stop_lat,stop_lon\n" +
		"1001,Main St,47.90,-122.20\n" +
		"9001,Weird Node,10.0,10.0\n"
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}
	if a.BBox == nil {
		t.Fatalf("BBox is nil, want a populated bbox")
	}
	want := BBox{MinLat: 10.0, MaxLat: 47.90, MinLon: -122.20, MaxLon: 10.0}
	if *a.BBox != want {
		t.Errorf("BBox = %+v, want %+v", *a.BBox, want)
	}
}

func TestAnalyzeZipNoValidStopsWarns(t *testing.T) {
	files := baseFixture()
	files["stops.txt"] = "stop_id,stop_name,stop_lat,stop_lon\n1001,Main St,,\n"
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}
	if a.BBox != nil {
		t.Errorf("BBox = %+v, want nil", a.BBox)
	}
	if !containsSubstring(a.Warnings, "no stops with valid coordinates") {
		t.Errorf("Warnings = %v, want a warning about missing coordinates", a.Warnings)
	}
}

func TestAnalyzeZipAgenciesWithoutAgencyID(t *testing.T) {
	files := baseFixture()
	files["agency.txt"] = "agency_name,agency_url,agency_timezone\n" +
		"Everett Transit,https://everetttransit.org,America/Los_Angeles\n"
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}
	want := []Agency{{AgencyID: "Everett Transit", Name: "Everett Transit"}}
	if !reflect.DeepEqual(a.Agencies, want) {
		t.Errorf("Agencies = %+v, want %+v", a.Agencies, want)
	}
}

func TestAnalyzeZipAgenciesWithBlankAgencyIDColumn(t *testing.T) {
	// agency_id column is present but blank for a row: unlike a missing
	// column, this is not defaulted to agency_name.
	files := baseFixture()
	files["agency.txt"] = "agency_id,agency_name\n,Everett Transit\n"
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}
	want := []Agency{{AgencyID: "", Name: "Everett Transit"}}
	if !reflect.DeepEqual(a.Agencies, want) {
		t.Errorf("Agencies = %+v, want %+v", a.Agencies, want)
	}
}

func TestAnalyzeZipSampleIDs(t *testing.T) {
	files := baseFixture()
	// Add a 4th stop/route to confirm sampling caps at 3.
	files["stops.txt"] += "1004,4th Ave,47.93,-122.23\n"
	zipPath := newGTFSZip(t, files)

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}

	wantStopIDs := []string{"1001", "1002", "1003"}
	if !reflect.DeepEqual(a.SampleIDs.StopID, wantStopIDs) {
		t.Errorf("SampleIDs.StopID = %v, want %v (capped at 3, in file order)", a.SampleIDs.StopID, wantStopIDs)
	}
	wantRouteIDs := []string{"100", "101"}
	if !reflect.DeepEqual(a.SampleIDs.RouteID, wantRouteIDs) {
		t.Errorf("SampleIDs.RouteID = %v, want %v", a.SampleIDs.RouteID, wantRouteIDs)
	}
	wantTripIDs := []string{"T1", "T2"}
	if !reflect.DeepEqual(a.SampleIDs.TripID, wantTripIDs) {
		t.Errorf("SampleIDs.TripID = %v, want %v", a.SampleIDs.TripID, wantTripIDs)
	}
}

func TestAnalyzeZipFileList(t *testing.T) {
	zipPath := newGTFSZip(t, baseFixture())

	a, err := AnalyzeZip(zipPath)
	if err != nil {
		t.Fatalf("AnalyzeZip() error: %v", err)
	}
	wantFiles := map[string]bool{
		"agency.txt": true, "stops.txt": true, "routes.txt": true,
		"trips.txt": true, "calendar.txt": true, "calendar_dates.txt": true,
	}
	if len(a.Files) != len(wantFiles) {
		t.Fatalf("Files = %v, want exactly %v", a.Files, wantFiles)
	}
	for _, f := range a.Files {
		if !wantFiles[f] {
			t.Errorf("unexpected file %q in Files", f)
		}
	}
}

func TestAnalyzeZipMissingZip(t *testing.T) {
	if _, err := AnalyzeZip("/does/not/exist.zip"); err == nil {
		t.Error("expected error for missing zip, got none")
	}
}

func containsSubstring(items []string, substr string) bool {
	for _, item := range items {
		if strings.Contains(item, substr) {
			return true
		}
	}
	return false
}
