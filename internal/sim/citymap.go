package sim

import "strings"

// Norrköping ground plan, matching the UI's OpenStreetMap snapshot (2026-10-16).
// One scene unit = 25 ground metres. X is east (SWEREF 99 TM grid east); Z is south.
// x = (E - 569030) / (25 * 0.9996584126), z = (6494989 - N) / (25 * 0.9996584126)
// with (E, N) in SWEREF 99 TM (EPSG:3006); see GeoReference.
// Building/park destinations use mapped footprint centres; district targets are approximate.
// Flight physics remain in simulation units, independently of the miniature's map scale.

type Place struct {
	Name string
	X, Z float64
}

// Hubs are fictional simulation depots; IDs retained for API compatibility.
var Hubs = []HubView{
	{"HUB_HAMNEN", "Hamndepån (simulerad)", []float64{59.139, 0.0, -57.2}},
	{"HUB_VRINNEVI", "Söderdepån (simulerad)", []float64{-16.94, 0.0, 62.445}},
}

var Places = []Place{
	{"Drottninggatan", 0.096, 4.805},
	{"Gamla torget", -3.732, 5.038},
	{"Rådhustorget", -0.9, -9.887},
	{"Sankt Olai kyrka", 4.518, -1.182},
	{"Arbetets museum", -17.405, 3.352},
	{"Louis De Geer-hallen", -8.357, 7.443},
	{"Visualiseringscentret", -25.691, 5.233},
	{"Campus Norrköping", -24.648, -0.01},
	{"Carl Johans park", -8.142, -21.566},
	{"Norrköping C", -9.548, -29.281},
	{"Saltängen", 19.15, -27.93},
	{"Söder Tull", 3.327, 17.379},
	{"Sankt Johannes kyrka", 23.886, 58.23},
	{"Himmelstalund", -70.363, -10.805},
	{"Nya Parken", -33.287, 25.341},
	{"Norrköpings hamn", 57.642, -29.104},
	{"Östra promenaden", 16.518, -2.103},
	{"Ljuraparken", 35.698, 39.752},
	{"Ljuraskolan", 30.252, 29.615},
	{"Konstmuseet", 8.413, 30.639},
	{"Centralbadet", 35.689, 23.774},
	{"Värmekyrkan", -15.001, 6.45},
	{"Matteus kyrka", -36.743, -13.933},
}

// FindPlace is a case-insensitive lookup; Parken aliases preserve old clients.
func FindPlace(name *string) *Place {
	if name == nil {
		return nil
	}
	wanted := strings.TrimSpace(*name)
	switch strings.ToLower(wanted) {
	case "idrottsparken", "parken":
		wanted = "Nya Parken"
	}
	for i := range Places {
		if strings.EqualFold(Places[i].Name, wanted) {
			return &Places[i]
		}
	}
	return nil
}
