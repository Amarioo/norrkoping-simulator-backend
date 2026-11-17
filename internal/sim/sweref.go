package sim

import "math"

// SWEREF 99 TM (EPSG:3006): Gauss–Krüger projection of GRS 80, central meridian 15° E, scale 0.9996,
// false easting 500 000 m. Lantmäteriet's Krüger series; same implementation as the UI's
// src/lib/geo/sweref99tm.js (verified against Lantmäteriet SWEPOS point G5TA, Lantmäteriet STAC tile
// corners and PROJ, see the UI's scripts/geo/verify_projection.py).

const (
	swerefCentralMeridian = 15.0
	swerefK0              = 0.9996
	swerefFalseEasting    = 500000.0
)

type swerefSeries struct {
	aroof          float64
	ca, cb, cc, cd float64
	as, bs, cs, ds float64
	beta, delta    [4]float64
}

// The coefficients are computed at run time in float64, as the Kotlin object does.
var sweref = func() swerefSeries {
	a, f := 6378137.0, 298.257222101
	f = 1 / f
	e2 := f * (2 - f)
	n := f / (2 - f)
	p := math.Pow
	return swerefSeries{
		aroof: a / (1 + n) * (1 + p(n, 2)/4 + p(n, 4)/64),
		ca:    e2,
		cb:    (5*p(e2, 2) - p(e2, 3)) / 6,
		cc:    (104*p(e2, 3) - 45*p(e2, 4)) / 120,
		cd:    1237 * p(e2, 4) / 1260,
		as:    e2 + p(e2, 2) + p(e2, 3) + p(e2, 4),
		bs:    -(7*p(e2, 2) + 17*p(e2, 3) + 30*p(e2, 4)) / 6,
		cs:    (224*p(e2, 3) + 889*p(e2, 4)) / 120,
		ds:    -4279 * p(e2, 4) / 1260,
		beta: [4]float64{
			n/2 - 2*p(n, 2)/3 + 5*p(n, 3)/16 + 41*p(n, 4)/180,
			13*p(n, 2)/48 - 3*p(n, 3)/5 + 557*p(n, 4)/1440,
			61*p(n, 3)/240 - 103*p(n, 4)/140,
			49561 * p(n, 4) / 161280,
		},
		delta: [4]float64{
			n/2 - 2*p(n, 2)/3 + 37*p(n, 3)/96 - p(n, 4)/360,
			p(n, 2)/48 + p(n, 3)/15 - 437*p(n, 4)/1440,
			17*p(n, 3)/480 - 37*p(n, 4)/840,
			4397 * p(n, 4) / 161280,
		},
	}
}()

func radians(degrees float64) float64 { return degrees / 180 * math.Pi }
func degrees(radians float64) float64 { return radians * 180 / math.Pi }

// SwerefForward converts geodetic degrees to (easting, northing) metres.
func SwerefForward(lat, lon float64) (easting, northing float64) {
	s := sweref
	phi := radians(lat)
	dl := radians(lon - swerefCentralMeridian)
	sin := math.Sin(phi)
	phis := phi - sin*math.Cos(phi)*(s.ca+s.cb*math.Pow(sin, 2)+s.cc*math.Pow(sin, 4)+s.cd*math.Pow(sin, 6))
	xi := math.Atan2(math.Tan(phis), math.Cos(dl))
	eta := math.Atanh(math.Cos(phis) * math.Sin(dl))
	x, y := xi, eta
	for j := 1; j <= 4; j++ {
		k := float64(2 * j)
		x += s.beta[j-1] * math.Sin(k*xi) * math.Cosh(k*eta)
		y += s.beta[j-1] * math.Cos(k*xi) * math.Sinh(k*eta)
	}
	return swerefK0*s.aroof*y + swerefFalseEasting, swerefK0 * s.aroof * x
}

// SwerefInverse converts (easting, northing) metres to geodetic (lat, lon) degrees.
func SwerefInverse(easting, northing float64) (lat, lon float64) {
	s := sweref
	xi := northing / (swerefK0 * s.aroof)
	eta := (easting - swerefFalseEasting) / (swerefK0 * s.aroof)
	xip, etap := xi, eta
	for j := 1; j <= 4; j++ {
		k := float64(2 * j)
		xip -= s.delta[j-1] * math.Sin(k*xi) * math.Cosh(k*eta)
		etap -= s.delta[j-1] * math.Cos(k*xi) * math.Sinh(k*eta)
	}
	phis := math.Asin(math.Sin(xip) / math.Cosh(etap))
	dl := math.Atan(math.Sinh(etap) / math.Cos(xip))
	sin := math.Sin(phis)
	phi := phis + sin*math.Cos(phis)*(s.as+s.bs*math.Pow(sin, 2)+s.cs*math.Pow(sin, 4)+s.ds*math.Pow(sin, 6))
	return degrees(phi), swerefCentralMeridian + degrees(dl)
}
