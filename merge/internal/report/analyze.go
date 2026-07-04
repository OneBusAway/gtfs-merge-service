package report

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxSampleIDs is the number of sample stop/route/trip IDs collected per
// feed (docs/config-schema.md §3.1's inputs[].sampleIds).
const maxSampleIDs = 3

// ZipAnalysis is the result of analyzing a single GTFS zip: file list,
// agencies, entity counts, service date range, stop bounding box, and a
// handful of sample IDs. Warnings collects non-fatal, free-text issues
// encountered while analyzing (e.g. a missing expected column), for the
// caller to fold into report.json's top-level warnings[] with feed context.
type ZipAnalysis struct {
	Files        []string
	Agencies     []Agency
	Counts       Counts
	ServiceRange ServiceRange
	BBox         *BBox
	SampleIDs    SampleIDs
	Warnings     []string
}

// zipIndex indexes a GTFS zip's entries by base filename (matching
// internal/validate's convention of ignoring any wrapping directory), while
// preserving the zip's on-disk entry order for the file list.
type zipIndex struct {
	byName map[string]*zip.File
	order  []string
}

func indexZip(r *zip.Reader) *zipIndex {
	zi := &zipIndex{byName: make(map[string]*zip.File, len(r.File))}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := baseName(f.Name)
		zi.byName[name] = f
		zi.order = append(zi.order, name)
	}
	return zi
}

func baseName(name string) string {
	if idx := strings.LastIndexByte(name, '/'); idx != -1 {
		return name[idx+1:]
	}
	return name
}

// openCSV opens filename's entry (if present) and returns a csv.Reader
// positioned at the start of the file, streaming directly off the zip
// entry's reader (never buffering the whole file). FieldsPerRecord is
// relaxed (-1) to tolerate real-world feeds with ragged trailing columns.
// The returned closer must be closed by the caller; found is false (with a
// nil reader/closer) if the zip has no such file.
func (zi *zipIndex) openCSV(filename string) (*csv.Reader, io.Closer, bool, error) {
	f, ok := zi.byName[filename]
	if !ok {
		return nil, nil, false, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to open %s: %w", filename, err)
	}
	cr := csv.NewReader(rc)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true
	return cr, rc, true, nil
}

// readHeader reads and normalizes filename's header row (trimming a
// leading UTF-8 BOM some GTFS producers prepend to the first column name).
// ok is false if the file is empty (no rows at all).
func readHeader(cr *csv.Reader) (header []string, ok bool, err error) {
	row, err := cr.Read()
	if err == io.EOF {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(row) > 0 {
		row[0] = strings.TrimPrefix(row[0], "\ufeff")
	}
	return row, true, nil
}

// colIndex returns the index of name within header, or -1 if absent.
func colIndex(header []string, name string) int {
	for i, h := range header {
		if strings.TrimSpace(h) == name {
			return i
		}
	}
	return -1
}

// col returns the trimmed value at idx within row, or "" if idx is out of
// range (missing trailing column) or negative (column absent entirely).
func col(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

// AnalyzeZip extracts report-relevant facts from the GTFS zip at zipPath.
// It streams each CSV entry via encoding/csv rather than loading whole
// files into memory, and never reads stop_times.txt (the analysis needs
// none of its columns, and it can be enormous).
func AnalyzeZip(zipPath string) (*ZipAnalysis, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip %s: %w", zipPath, err)
	}
	defer func() {
		_ = r.Close()
	}()

	zi := indexZip(&r.Reader)

	a := &ZipAnalysis{
		Files:     append([]string{}, zi.order...),
		Agencies:  []Agency{},
		SampleIDs: SampleIDs{StopID: []string{}, RouteID: []string{}, TripID: []string{}},
	}

	if err := a.parseAgencies(zi); err != nil {
		return nil, err
	}

	calendarServiceIDs, err := a.parseCalendar(zi)
	if err != nil {
		return nil, err
	}
	if err := a.parseCalendarDates(zi, calendarServiceIDs); err != nil {
		return nil, err
	}

	if err := a.parseStops(zi); err != nil {
		return nil, err
	}
	if err := a.parseRoutes(zi); err != nil {
		return nil, err
	}
	if err := a.parseTrips(zi); err != nil {
		return nil, err
	}

	return a, nil
}

func (a *ZipAnalysis) warnf(format string, args ...any) {
	a.Warnings = append(a.Warnings, fmt.Sprintf(format, args...))
}

// parseAgencies populates Agencies from agency.txt. agency_id is optional
// in GTFS for single-agency feeds; when the column is entirely absent, it
// defaults to agency_name (or "" if that's absent too).
func (a *ZipAnalysis) parseAgencies(zi *zipIndex) error {
	cr, rc, found, err := zi.openCSV("agency.txt")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return fmt.Errorf("failed to read agency.txt header: %w", err)
	}
	if !ok {
		return nil
	}

	idIdx := colIndex(header, "agency_id")
	nameIdx := colIndex(header, "agency_name")
	if idIdx == -1 {
		a.warnf("agency.txt missing agency_id column; defaulting to agency_name")
	}
	if nameIdx == -1 {
		a.warnf("agency.txt missing agency_name column")
	}

	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read agency.txt: %w", err)
		}
		name := col(row, nameIdx)
		id := name
		if idIdx != -1 {
			id = col(row, idIdx)
		}
		a.Agencies = append(a.Agencies, Agency{AgencyID: id, Name: name})
	}
	return nil
}

// parseCalendar populates Counts.Calendars (partially; see
// parseCalendarDates) and ServiceRange from calendar.txt, returning the set
// of service_ids it saw so parseCalendarDates can find the ones that are
// new.
func (a *ZipAnalysis) parseCalendar(zi *zipIndex) (map[string]bool, error) {
	serviceIDs := make(map[string]bool)

	cr, rc, found, err := zi.openCSV("calendar.txt")
	if err != nil {
		return nil, err
	}
	if !found {
		return serviceIDs, nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return nil, fmt.Errorf("failed to read calendar.txt header: %w", err)
	}
	if !ok {
		return serviceIDs, nil
	}

	svcIdx := colIndex(header, "service_id")
	startIdx := colIndex(header, "start_date")
	endIdx := colIndex(header, "end_date")
	if startIdx == -1 || endIdx == -1 {
		a.warnf("calendar.txt missing start_date/end_date columns")
	}

	var minStart, maxEnd string
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read calendar.txt: %w", err)
		}
		a.Counts.Calendars++

		if svc := col(row, svcIdx); svc != "" {
			serviceIDs[svc] = true
		}

		if start := col(row, startIdx); start != "" && (minStart == "" || start < minStart) {
			minStart = start
		}
		if end := col(row, endIdx); end != "" && end > maxEnd {
			maxEnd = end
		}
	}
	a.ServiceRange.Start = minStart
	a.ServiceRange.End = maxEnd
	return serviceIDs, nil
}

// parseCalendarDates adds calendar_dates.txt-only service_ids (those not
// already seen in calendar.txt) to Counts.Calendars, and falls back to
// deriving ServiceRange from calendar_dates.txt's date column when
// calendar.txt produced no range (missing file, or no valid rows).
func (a *ZipAnalysis) parseCalendarDates(zi *zipIndex, calendarServiceIDs map[string]bool) error {
	cr, rc, found, err := zi.openCSV("calendar_dates.txt")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return fmt.Errorf("failed to read calendar_dates.txt header: %w", err)
	}
	if !ok {
		return nil
	}

	svcIdx := colIndex(header, "service_id")
	dateIdx := colIndex(header, "date")
	if dateIdx == -1 {
		a.warnf("calendar_dates.txt missing date column")
	}

	extraServiceIDs := make(map[string]bool)
	var minDate, maxDate string
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read calendar_dates.txt: %w", err)
		}

		if svc := col(row, svcIdx); svc != "" && !calendarServiceIDs[svc] {
			extraServiceIDs[svc] = true
		}

		if date := col(row, dateIdx); date != "" {
			if minDate == "" || date < minDate {
				minDate = date
			}
			if date > maxDate {
				maxDate = date
			}
		}
	}

	a.Counts.Calendars += len(extraServiceIDs)

	if a.ServiceRange.Start == "" && a.ServiceRange.End == "" {
		a.ServiceRange.Start = minDate
		a.ServiceRange.End = maxDate
	}
	return nil
}

// parseStops populates Counts.Stops, BBox, and SampleIDs.StopID from
// stops.txt. Rows with a blank/unparseable lat or lon are skipped for
// bbox purposes; when a location_type column is present, generic-node and
// boundary rows (location_type 3 or 4) are also excluded from the bbox,
// since those aren't necessarily point-like physical stops. If
// location_type is absent (or unparseable), every row with valid
// coordinates is simply included — see the milestone's bbox rule.
func (a *ZipAnalysis) parseStops(zi *zipIndex) error {
	cr, rc, found, err := zi.openCSV("stops.txt")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return fmt.Errorf("failed to read stops.txt header: %w", err)
	}
	if !ok {
		return nil
	}

	idIdx := colIndex(header, "stop_id")
	latIdx := colIndex(header, "stop_lat")
	lonIdx := colIndex(header, "stop_lon")
	locTypeIdx := colIndex(header, "location_type")
	if latIdx == -1 || lonIdx == -1 {
		a.warnf("stops.txt missing stop_lat/stop_lon column")
	}

	var bbox BBox
	haveBBox := false

	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read stops.txt: %w", err)
		}
		a.Counts.Stops++

		if id := col(row, idIdx); id != "" && len(a.SampleIDs.StopID) < maxSampleIDs {
			a.SampleIDs.StopID = append(a.SampleIDs.StopID, id)
		}

		if locTypeIdx != -1 {
			if lt, err := strconv.Atoi(col(row, locTypeIdx)); err == nil && lt >= 3 {
				continue
			}
		}

		lat, latErr := strconv.ParseFloat(col(row, latIdx), 64)
		lon, lonErr := strconv.ParseFloat(col(row, lonIdx), 64)
		if latErr != nil || lonErr != nil {
			continue
		}

		if !haveBBox {
			bbox = BBox{MinLat: lat, MaxLat: lat, MinLon: lon, MaxLon: lon}
			haveBBox = true
			continue
		}
		bbox.MinLat = min(bbox.MinLat, lat)
		bbox.MaxLat = max(bbox.MaxLat, lat)
		bbox.MinLon = min(bbox.MinLon, lon)
		bbox.MaxLon = max(bbox.MaxLon, lon)
	}

	if haveBBox {
		a.BBox = &bbox
	} else {
		a.warnf("stops.txt has no stops with valid coordinates")
	}
	return nil
}

func (a *ZipAnalysis) parseRoutes(zi *zipIndex) error {
	cr, rc, found, err := zi.openCSV("routes.txt")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return fmt.Errorf("failed to read routes.txt header: %w", err)
	}
	if !ok {
		return nil
	}
	idIdx := colIndex(header, "route_id")

	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read routes.txt: %w", err)
		}
		a.Counts.Routes++
		if id := col(row, idIdx); id != "" && len(a.SampleIDs.RouteID) < maxSampleIDs {
			a.SampleIDs.RouteID = append(a.SampleIDs.RouteID, id)
		}
	}
	return nil
}

func (a *ZipAnalysis) parseTrips(zi *zipIndex) error {
	cr, rc, found, err := zi.openCSV("trips.txt")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	defer func() {
		_ = rc.Close()
	}()

	header, ok, err := readHeader(cr)
	if err != nil {
		return fmt.Errorf("failed to read trips.txt header: %w", err)
	}
	if !ok {
		return nil
	}
	idIdx := colIndex(header, "trip_id")

	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read trips.txt: %w", err)
		}
		a.Counts.Trips++
		if id := col(row, idIdx); id != "" && len(a.SampleIDs.TripID) < maxSampleIDs {
			a.SampleIDs.TripID = append(a.SampleIDs.TripID, id)
		}
	}
	return nil
}
