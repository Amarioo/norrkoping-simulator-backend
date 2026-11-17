package sim

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	resources "norrkoping-simulator/src/main/resources"
)

// GeoReference is the horizontal scene contract (terrain dataset version 2), shared with the UI's
// terrain.ts: scene = (SWEREF 99 TM - origin) / (metresPerUnit * gridScaleFactor); x east, z south.
type GeoReference struct {
	CRS                    string  `json:"crs"`
	OriginEasting          float64 `json:"originEasting"`
	OriginNorthing         float64 `json:"originNorthing"`
	GridScaleFactor        float64 `json:"gridScaleFactor"`
	GridConvergenceDegrees float64 `json:"gridConvergenceDegrees"`
	MetresPerUnit          float64 `json:"metresPerUnit"`
	VerticalDatum          string  `json:"verticalDatum"`
	VerticalOriginMetres   float64 `json:"verticalOriginMetres"`
}

type TerrainTile struct {
	ID                     string     `json:"id"`
	Source                 string     `json:"source"`
	SourceURL              string     `json:"sourceUrl"`
	Licence                string     `json:"licence"`
	Acquired               string     `json:"acquired"`
	Kind                   string     `json:"kind"`
	ResolutionMetres       float64    `json:"resolutionMetres"`
	VerticalAccuracyMetres *float64   `json:"verticalAccuracyMetres"`
	VerticalDatum          string     `json:"verticalDatum"`
	Processing             string     `json:"processing"`
	X                      float64    `json:"x"`
	Z                      float64    `json:"z"`
	Step                   float64    `json:"step"`
	Columns                int        `json:"columns"`
	Rows                   int        `json:"rows"`
	Heights                []*float64 `json:"heights"`
	HeightsBase64          *string    `json:"heightsBase64"`
}

// SurfaceMeshData is the rendered ground of a DTM revision: an adaptive RTIN over the tile's 5 m nodes
// (UI surfaceMesh.ts, scripts/prepare-terrain-mesh.py). A triangle (right angle R, hypotenuse A-B, in
// nodes) is split at the hypotenuse midpoint M when M's bit is set, into (M; R, A) and (M; B, R).
type SurfaceMeshData struct {
	Kind         string          `json:"kind"`
	Tile         string          `json:"tile"`
	X            float64         `json:"x"`
	Z            float64         `json:"z"`
	Step         float64         `json:"step"`
	Depth        int             `json:"depth"`
	Columns      int             `json:"columns"`
	Rows         int             `json:"rows"`
	ActiveBase64 string          `json:"activeBase64"`
	Patches      *SurfacePatches `json:"patches"`
}

// SurfacePatches are road breakline patches (UI scripts/geo/road_patches.py and surfaceMesh.ts): RTIN
// leaves re-triangulated with the asphalt outline as mesh edges. Each patch triangle lies in one leaf.
type SurfacePatches struct {
	Kind            string `json:"kind"`
	Leaves          int    `json:"leaves"`
	Triangles       int    `json:"triangles"`
	VertexCount     int    `json:"vertexCount"`
	LeavesBase64    string `json:"leavesBase64"`
	VerticesBase64  string `json:"verticesBase64"`
	TrianglesBase64 string `json:"trianglesBase64"`
	// Integrated lists grounded linear road segments absorbed into the patches: draped on the surface.
	Integrated []string `json:"integrated"`
}

type TerrainDataset struct {
	Version   int              `json:"version"`
	Revision  string           `json:"revision"`
	Reference GeoReference     `json:"reference"`
	Tiles     []TerrainTile    `json:"tiles"`
	Mesh      *SurfaceMeshData `json:"mesh"`
}

type GroundSample struct {
	SceneY           float64
	MetresRH2000     float64
	SourceID         string
	ResolutionMetres float64
	Quality          string
}

const DatasetVersion = 2

// Pre-DTM flat-scene levels (scene units); see terrain.ts LEGACY_LEVELS. They are variables so that sums
// and differences of them are float64 arithmetic, as in the Kotlin implementation.
var (
	LegacyGround = -.02
	LegacyRoad   = .071
	LegacyStreet = .077

	streetAboveGround = LegacyStreet - LegacyGround
	streetAboveRoad   = LegacyStreet - LegacyRoad
)

type terrainTile struct {
	*TerrainTile
	binary []float32
}

func (t *terrainTile) height(i int) (float64, bool) {
	if t.binary != nil {
		v := float64(t.binary[i])
		return v, isFinite(v)
	}
	if h := t.Heights[i]; h != nil {
		return *h, true
	}
	return 0, false
}

type patchLeaf struct{ first, count int }

// TerrainService has the same grid contract and interpolation as the UI's terrain.ts. One vertical
// scale, no synthetic hills.
type TerrainService struct {
	Data  *TerrainDataset
	tiles []*terrainTile
	unit  float64
	dtm   bool // revision "lm-dtm-…": measured ground with the adaptive surface mesh

	mesh     *SurfaceMeshData
	meshTile *terrainTile
	meshBits []byte
	meshRoot int
	meshCol0 int
	meshRow0 int

	patchLeaves            map[int]patchLeaf
	patchX, patchZ, patchY []float64
	patchTris              []int32
	integratedSegments     map[string]bool

	cacheMu     sync.Mutex
	groundCache map[[2]float64]float64
}

func terrainError(format string, args ...any) error {
	return &ValidationError{"Invalid terrain: " + fmt.Sprintf(format, args...)}
}

func NewTerrainService(data *TerrainDataset) (*TerrainService, error) {
	s := &TerrainService{Data: data, groundCache: map[[2]float64]float64{}}
	r := data.Reference
	seen := map[string]bool{}
	for i := range data.Tiles {
		t := &terrainTile{TerrainTile: &data.Tiles[i]}
		if t.HeightsBase64 != nil {
			raw, err := base64.StdEncoding.DecodeString(*t.HeightsBase64)
			if err != nil || int64(len(raw)) != int64(t.Columns)*int64(t.Rows)*4 {
				return nil, terrainError("tile %s has a broken binary grid", t.ID)
			}
			t.binary = make([]float32, len(raw)/4)
			for k := range t.binary {
				v := math.Float32frombits(binary.LittleEndian.Uint32(raw[k*4:]))
				if math.IsInf(float64(v), 0) {
					return nil, terrainError("tile %s has an infinite height", t.ID)
				}
				t.binary[k] = v
			}
		}
		s.tiles = append(s.tiles, t)
	}
	if data.Version != DatasetVersion || strings.TrimSpace(data.Revision) == "" || r.VerticalDatum != "RH2000" || r.CRS != "EPSG:3006" {
		return nil, terrainError("unsupported dataset version, revision, datum or CRS")
	}
	for _, v := range []float64{r.OriginEasting, r.OriginNorthing, r.GridConvergenceDegrees, r.VerticalOriginMetres} {
		if !isFinite(v) {
			return nil, terrainError("reference is not finite")
		}
	}
	if !isFinite(r.MetresPerUnit) || r.MetresPerUnit <= 0 || !isFinite(r.GridScaleFactor) || r.GridScaleFactor < 0.9995 || r.GridScaleFactor > 1.002 {
		return nil, terrainError("reference scale is out of range")
	}
	for _, t := range s.tiles {
		if seen[t.ID] {
			return nil, terrainError("duplicate tile %s", t.ID)
		}
		seen[t.ID] = true
		for _, text := range []string{t.ID, t.Source, t.SourceURL, t.Licence, t.Acquired, t.Processing} {
			if strings.TrimSpace(text) == "" {
				return nil, terrainError("tile %s lacks provenance", t.ID)
			}
		}
		if (t.Kind != "DTM" && t.Kind != "DSM-fallback" && t.Kind != "water") || t.VerticalDatum != r.VerticalDatum {
			return nil, terrainError("tile %s has another kind or datum", t.ID)
		}
		if !isFinite(t.X) || !isFinite(t.Z) || !isFinite(t.Step) || t.Step <= 0 || !isFinite(t.ResolutionMetres) || t.ResolutionMetres <= 0 {
			return nil, terrainError("tile %s has a broken grid", t.ID)
		}
		if a := t.VerticalAccuracyMetres; a != nil && (!isFinite(*a) || *a < 0) {
			return nil, terrainError("tile %s has a broken accuracy", t.ID)
		}
		size := len(t.Heights)
		if t.binary != nil {
			size = len(t.binary)
		}
		if t.Columns < 2 || t.Rows < 2 || int64(t.Columns)*int64(t.Rows) != int64(size) {
			return nil, terrainError("tile %s has a broken grid size", t.ID)
		}
		for _, h := range t.Heights {
			if h != nil && !isFinite(*h) {
				return nil, terrainError("tile %s has a height that is not finite", t.ID)
			}
		}
	}
	// Measured ground first, then the finest and most accurate tile.
	accuracy := func(t *terrainTile) float64 {
		if t.VerticalAccuracyMetres == nil {
			return math.Inf(1)
		}
		return *t.VerticalAccuracyMetres
	}
	sort.SliceStable(s.tiles, func(i, j int) bool {
		a, b := s.tiles[i], s.tiles[j]
		if (a.Kind != "DTM") != (b.Kind != "DTM") {
			return a.Kind == "DTM"
		}
		if a.ResolutionMetres != b.ResolutionMetres {
			return a.ResolutionMetres < b.ResolutionMetres
		}
		if accuracy(a) != accuracy(b) {
			return accuracy(a) < accuracy(b)
		}
		return a.ID < b.ID
	})
	s.unit = r.MetresPerUnit * r.GridScaleFactor
	s.dtm = strings.HasPrefix(data.Revision, "lm-dtm-")
	if s.dtm {
		if data.Mesh == nil {
			return nil, terrainError("DTM revision without surface mesh")
		}
		if err := s.loadMesh(data.Mesh); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *TerrainService) loadMesh(m *SurfaceMeshData) error {
	for _, t := range s.tiles {
		if t.ID == m.Tile {
			s.meshTile = t
			break
		}
	}
	bits, err := base64.StdEncoding.DecodeString(m.ActiveBase64)
	if err != nil || s.meshTile == nil {
		return terrainError("surface mesh has no tile or a broken mask")
	}
	root := 1 << m.Depth
	t := s.meshTile
	if m.Kind != "rtin" || t.Kind != "DTM" || math.Abs(m.Step-t.Step) >= 1e-12 ||
		m.Depth < 1 || m.Depth > 12 || (m.Columns-1)%root != 0 || (m.Rows-1)%root != 0 ||
		int64(len(bits)) != (int64(m.Columns)*int64(m.Rows)+7)/8 {
		return terrainError("surface mesh does not match its tile")
	}
	s.mesh, s.meshBits, s.meshRoot = m, bits, root
	s.meshCol0 = int(math.RoundToEven((t.X - m.X) / m.Step))
	s.meshRow0 = int(math.RoundToEven((t.Z - m.Z) / m.Step))
	p := m.Patches
	if p == nil {
		return nil
	}
	decode := func(text string) []byte { raw, _ := base64.StdEncoding.DecodeString(text); return raw }
	l, v, tris := decode(p.LeavesBase64), decode(p.VerticesBase64), decode(p.TrianglesBase64)
	if len(l) != p.Leaves*12 || len(v) != p.VertexCount*24 || len(tris) != p.Triangles*12 {
		return terrainError("invalid surface patches")
	}
	i32 := func(raw []byte, at int) int { return int(int32(binary.LittleEndian.Uint32(raw[at:]))) }
	f64 := func(raw []byte, at int) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(raw[at:])) }
	s.patchLeaves = make(map[int]patchLeaf, p.Leaves)
	for i := 0; i < p.Leaves; i++ {
		s.patchLeaves[i32(l, i*12)] = patchLeaf{i32(l, i*12+4), i32(l, i*12+8)}
	}
	s.patchX, s.patchZ, s.patchY = make([]float64, p.VertexCount), make([]float64, p.VertexCount), make([]float64, p.VertexCount)
	for i := 0; i < p.VertexCount; i++ {
		s.patchX[i], s.patchZ[i], s.patchY[i] = f64(v, i*24), f64(v, i*24+8), s.ToSceneHeight(f64(v, i*24+16))
	}
	s.patchTris = make([]int32, p.Triangles*3)
	for i := range s.patchTris {
		index := i32(tris, i*4)
		if index < 0 || index >= p.VertexCount {
			return terrainError("invalid surface patch triangle")
		}
		s.patchTris[i] = int32(index)
	}
	s.integratedSegments = make(map[string]bool, len(p.Integrated))
	for _, key := range p.Integrated {
		s.integratedSegments[key] = true
	}
	return nil
}

// FromSweref converts SWEREF 99 TM (easting, northing) to scene (x, z).
func (s *TerrainService) FromSweref(easting, northing float64) (x, z float64) {
	r := s.Data.Reference
	return (easting - r.OriginEasting) / s.unit, (r.OriginNorthing - northing) / s.unit
}

// ToSweref converts scene (x, z) to SWEREF 99 TM (easting, northing).
func (s *TerrainService) ToSweref(x, z float64) (easting, northing float64) {
	r := s.Data.Reference
	return r.OriginEasting + x*s.unit, r.OriginNorthing - z*s.unit
}

// Project converts geodetic degrees to scene (x, z).
func (s *TerrainService) Project(lat, lon float64) (x, z float64) {
	return s.FromSweref(SwerefForward(lat, lon))
}

// Unproject converts scene (x, z) to geodetic (lat, lon) degrees.
func (s *TerrainService) Unproject(x, z float64) (lat, lon float64) {
	return SwerefInverse(s.ToSweref(x, z))
}

func (s *TerrainService) ToSceneHeight(metres float64) float64 {
	return (metres - s.Data.Reference.VerticalOriginMetres) / s.Data.Reference.MetresPerUnit
}

// Sample interpolates the best tile that covers (x, z) and has data there; nil when none does.
func (s *TerrainService) Sample(x, z float64, water bool) *GroundSample {
	for _, tile := range s.tiles {
		if (tile.Kind == "water") != water {
			continue
		}
		u, v := (x-tile.X)/tile.Step, (z-tile.Z)/tile.Step
		if u < 0 || v < 0 || u > float64(tile.Columns-1) || v > float64(tile.Rows-1) {
			continue
		}
		col := min(floorInt(u), tile.Columns-2)
		row := min(floorInt(v), tile.Rows-2)
		tx, tz := u-float64(col), v-float64(row)
		indices := [4]int{row*tile.Columns + col, row*tile.Columns + col + 1, (row+1)*tile.Columns + col, (row+1)*tile.Columns + col + 1}
		weights := [4]float64{(1 - tx) * (1 - tz), tx * (1 - tz), (1 - tx) * tz, tx * tz}
		metres, complete := 0.0, true
		for i := range indices {
			h, ok := tile.height(indices[i])
			if !ok {
				if weights[i] > 0 {
					complete = false
					break
				}
				h = 0.0
			}
			metres += h * weights[i]
		}
		if !complete {
			continue
		}
		quality := "surface-fallback"
		if tile.Kind == "DTM" {
			quality = "measured-ground"
		}
		return &GroundSample{s.ToSceneHeight(metres), metres, tile.ID, tile.ResolutionMetres, quality}
	}
	return nil
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// leafKey is the same key as surfaceMesh.ts leafKey(): right-angle node and the direction to the hypotenuse.
func (s *TerrainService) leafKey(rx, rz, ax, az, bx, bz int) int {
	return (rz*s.mesh.Columns+rx)*9 + (sign(ax+bx-2*rx)+1)*3 + (sign(az+bz-2*rz) + 1)
}

// patchHeight is the height in a patched leaf: the triangle containing (gx, gz), as surfaceMesh.ts Patches.height().
func (s *TerrainService) patchHeight(leaf patchLeaf, gx, gz float64) float64 {
	best, height := math.Inf(-1), math.NaN()
	for t := leaf.first; t < leaf.first+leaf.count; t++ {
		a, b, c := s.patchTris[t*3], s.patchTris[t*3+1], s.patchTris[t*3+2]
		e1x, e1z := s.patchX[b]-s.patchX[a], s.patchZ[b]-s.patchZ[a]
		e2x, e2z := s.patchX[c]-s.patchX[a], s.patchZ[c]-s.patchZ[a]
		px, pz := gx-s.patchX[a], gz-s.patchZ[a]
		det := e1x*e2z - e1z*e2x
		wb, wc := (px*e2z-pz*e2x)/det, (e1x*pz-e1z*px)/det
		inside := math.Min(math.Min(1-wb-wc, wb), wc)
		if inside > best {
			best = inside
			height = s.patchY[a] + wb*(s.patchY[b]-s.patchY[a]) + wc*(s.patchY[c]-s.patchY[a])
			if inside >= 0 {
				break
			}
		}
	}
	return height
}

func (s *TerrainService) meshActive(i, j int) bool {
	k := j*s.mesh.Columns + i
	return (s.meshBits[k>>3]>>(k&7))&1 == 1
}

// meshNode is the scene height of mesh node (i, j): the tile node, clamped at the tile edges.
func (s *TerrainService) meshNode(i, j int) float64 {
	t := s.meshTile
	col := clampInt(i-s.meshCol0, 0, t.Columns-1)
	row := clampInt(j-s.meshRow0, 0, t.Rows-1)
	h, _ := t.height(row*t.Columns + col)
	return s.ToSceneHeight(h)
}

// SurfaceHeight is the rendered ground (scene units): the adaptive mesh, identical to the UI's
// SurfaceMesh.height().
func (s *TerrainService) SurfaceHeight(x, z float64) float64 {
	m := s.mesh
	if m == nil {
		if sample := s.Sample(x, z, false); sample != nil {
			return sample.SceneY
		}
		return -.02
	}
	gx, gz := (x-m.X)/m.Step, (z-m.Z)/m.Step
	r := s.meshRoot
	ri := clampInt(floorInt(gx/float64(r)), 0, (m.Columns-1)/r-1)
	rj := clampInt(floorInt(gz/float64(r)), 0, (m.Rows-1)/r-1)
	i0, j0 := ri*r, rj*r
	u, v := gx-float64(i0), gz-float64(j0)
	var rx, rz, ax, az, bx, bz int
	if (ri+rj)%2 == 0 {
		if u >= v {
			rx, rz, ax, az, bx, bz = i0+r, j0, i0, j0, i0+r, j0+r
		} else {
			rx, rz, ax, az, bx, bz = i0, j0+r, i0+r, j0+r, i0, j0
		}
	} else if u+v <= float64(r) {
		rx, rz, ax, az, bx, bz = i0, j0, i0+r, j0, i0, j0+r
	} else {
		rx, rz, ax, az, bx, bz = i0+r, j0+r, i0, j0+r, i0+r, j0
	}
	for {
		sx, sz := ax+bx, az+bz
		if sx&1 != 0 || sz&1 != 0 {
			break
		}
		mx, mz := sx>>1, sz>>1
		if !s.meshActive(mx, mz) {
			break
		}
		side := float64(mx-rx)*(gz-float64(rz)) - float64(mz-rz)*(gx-float64(rx))
		sideA := (mx-rx)*(az-rz) - (mz-rz)*(ax-rx)
		if (side >= 0) == (sideA >= 0) {
			bx, bz, ax, az = ax, az, rx, rz
		} else {
			ax, az, bx, bz = bx, bz, rx, rz
		}
		rx, rz = mx, mz
	}
	if leaf, patched := s.patchLeaves[s.leafKey(rx, rz, ax, az, bx, bz)]; patched {
		return s.patchHeight(leaf, gx, gz)
	}
	e1x, e1z := float64(ax-rx), float64(az-rz)
	e2x, e2z := float64(bx-rx), float64(bz-rz)
	px, pz := gx-float64(rx), gz-float64(rz)
	det := e1x*e2z - e1z*e2x
	wa, wb := (px*e2z-pz*e2x)/det, (e1x*pz-e1z*px)/det
	hr := s.meshNode(rx, rz)
	return hr + wa*(s.meshNode(ax, az)-hr) + wb*(s.meshNode(bx, bz)-hr)
}

// GroundHeight is the ground the scene is drawn on. Outside measured data it falls back to the nearest
// DTM nodes, and finally to the legacy ground plane.
func (s *TerrainService) GroundHeight(x, z float64) float64 {
	if sample := s.Sample(x, z, false); sample != nil {
		if s.dtm {
			return s.SurfaceHeight(x, z)
		}
		return sample.SceneY
	}
	key := [2]float64{x, z}
	s.cacheMu.Lock()
	cached, ok := s.groundCache[key]
	s.cacheMu.Unlock()
	if ok {
		return cached
	}
	type candidate struct{ distance, height float64 }
	for _, tile := range s.tiles {
		if tile.Kind != "DTM" {
			continue
		}
		u, v := (x-tile.X)/tile.Step, (z-tile.Z)/tile.Step
		if u < 0 || v < 0 || u > float64(tile.Columns-1) || v > float64(tile.Rows-1) {
			continue
		}
		for _, radius := range []int{2, 4, 8, 16, 32} {
			var candidates []candidate
			for r := max(floorInt(v)-radius, 0); r <= min(int(math.Ceil(v))+radius, tile.Rows-1); r++ {
				for c := max(floorInt(u)-radius, 0); c <= min(int(math.Ceil(u))+radius, tile.Columns-1); c++ {
					h, ok := tile.height(r*tile.Columns + c)
					if !ok {
						continue
					}
					du, dv := u-float64(c), v-float64(r)
					candidates = append(candidates, candidate{du*du + dv*dv, h})
				}
			}
			if len(candidates) == 0 {
				continue
			}
			sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].distance < candidates[j].distance })
			sum, weight := 0.0, 0.0
			for _, c := range candidates[:min(4, len(candidates))] {
				w := 1.0 / math.Max(c.distance, 1e-9)
				sum += c.height * w
				weight += w
			}
			y := s.ToSceneHeight(sum / weight)
			s.cacheMu.Lock()
			if len(s.groundCache) < 100000 {
				s.groundCache[key] = y
			}
			s.cacheMu.Unlock()
			return y
		}
	}
	return -.02
}

// RelativeHeight is the legacy flat-scene conversion (ground plane −0.02 onto the terrain); non-DTM preview only.
func (s *TerrainService) RelativeHeight(x, z, legacyY float64) float64 {
	return legacyY + s.GroundHeight(x, z) + .02
}

// PlaceLegacy is the same as terrain.ts placeLegacy(): a legacy model point on the surface it was modelled on.
func (s *TerrainService) PlaceLegacy(x, z, legacyY, standsOn float64) float64 {
	if !s.dtm {
		return s.RelativeHeight(x, z, legacyY)
	}
	return s.GroundHeight(x, z) + legacyY - standsOn
}

type bridgeProfileData struct {
	Points map[string]float64     `json:"points"`
	Spans  map[string][][]float64 `json:"spans"`
}

// `spanInfo` documents the spans for people and tests; the server needs `points` and `spans`.
var bridgeProfiles = sync.OnceValue(func() *bridgeProfileData {
	var data bridgeProfileData
	mustDecodeResource("bridge-profiles.json", &data)
	return &data
})

func pointKey(values ...float64) string {
	key := make([]byte, 0, 32)
	for i, v := range values {
		if i > 0 {
			key = append(key, ',')
		}
		key = appendJavaFixed(key, v, 3)
	}
	return string(key)
}

// BridgeHeight is the surveyed or estimated deck height of a profiled network point, if it has one.
func (s *TerrainService) BridgeHeight(x, z, legacyY float64) (float64, bool) {
	if !s.dtm {
		return 0, false
	}
	h, ok := bridgeProfiles().Points[pointKey(x, z, legacyY)]
	return h, ok
}

// RoadHeight is the network reference height, identical to terrain.ts roadHeight(): with the DTM the
// legacy street surface (LEGACY_STREET above LEGACY_GROUND, 2.43 m) is removed from every road/rail
// height, including the estimated bridge profiles. Legacy rises (elevated structures) are kept.
func (s *TerrainService) RoadHeight(x, z, legacyY float64) float64 {
	if !s.dtm {
		return s.RelativeHeight(x, z, legacyY)
	}
	if h, ok := s.BridgeHeight(x, z, legacyY); ok {
		return h - streetAboveGround
	}
	return s.RelativeHeight(x, z, math.Max(legacyY, LegacyRoad)) - streetAboveGround
}

// span is an engineered span of a segment (bridge-profiles.json `spans`, rules v4): an at-grade way
// bridging an underpass or culvert trench linearly. Same lookup as terrain.ts spans().
func (s *TerrainService) span(ax, az, bx, bz float64) (pieces [][]float64, reversed bool) {
	if !s.dtm {
		return nil, false
	}
	a, b := pointKey(ax, az), pointKey(bx, bz)
	if pieces, ok := bridgeProfiles().Spans[a+"|"+b]; ok {
		return pieces, false
	}
	if pieces, ok := bridgeProfiles().Spans[b+"|"+a]; ok {
		return pieces, true
	}
	return nil, false
}

func spanAt(pieces [][]float64, u float64) (float64, bool) {
	for _, p := range pieces {
		t0, t1, y0, y1 := p[0], p[1], p[2], p[3]
		if u >= t0-1e-9 && u <= t1+1e-9 {
			if t1-t0 < 1e-12 {
				return y0, true
			}
			return y0 + (y1-y0)*(u-t0)/(t1-t0), true
		}
	}
	return 0, false
}

// SpanHeight is the engineered span height of the segment a->b at u, if a span covers u.
func (s *TerrainService) SpanHeight(ax, az, bx, bz, u float64) (float64, bool) {
	pieces, reversed := s.span(ax, az, bx, bz)
	if pieces == nil {
		return 0, false
	}
	if reversed {
		u = 1 - u
	}
	return spanAt(pieces, u)
}

// LinearBetween is true for a linear (deck) profile, only between two profiled ends; identical to
// terrain.ts linearBetween(). Points are [x, legacyY, z].
func (s *TerrainService) LinearBetween(a, b []float64) bool {
	if !s.dtm {
		return true
	}
	_, profiledA := s.BridgeHeight(a[0], a[2], a[1])
	_, profiledB := s.BridgeHeight(b[0], b[2], b[1])
	return profiledA && profiledB
}

// Integrated is the same as terrain.ts integrated(): a segment absorbed into a road breakline patch.
func (s *TerrainService) Integrated(ax, az, bx, bz float64) bool {
	if len(s.integratedSegments) == 0 {
		return false
	}
	a, b := pointKey(ax, az), pointKey(bx, bz)
	return s.integratedSegments[a+"|"+b] || s.integratedSegments[b+"|"+a]
}

// SegmentProfile is the height rule of one network segment a->b (points [x, legacyY, z]). The rule is
// fixed per segment, so traffic resolves it once and then only evaluates Height per vehicle.
type SegmentProfile struct {
	terrain    *TerrainService
	a, b       []float64
	legacy     bool // non-DTM preview: interpolated legacy height on the ground
	integrated bool
	pieces     [][]float64
	reversed   bool
	linear     bool
	ya, yb     float64
	offA, offB float64
}

// Segment resolves the height rule between two network points.
func (s *TerrainService) Segment(a, b []float64) *SegmentProfile {
	p := &SegmentProfile{terrain: s, a: a, b: b}
	if !s.dtm {
		p.legacy = true
		return p
	}
	if s.Integrated(a[0], a[2], b[0], b[2]) {
		p.integrated = true
		return p
	}
	p.pieces, p.reversed = s.span(a[0], a[2], b[0], b[2])
	p.ya, p.yb = s.RoadHeight(a[0], a[2], a[1]), s.RoadHeight(b[0], b[2], b[1])
	p.linear = s.LinearBetween(a, b)
	if !p.linear {
		p.offA, p.offB = p.ya-s.GroundHeight(a[0], a[2]), p.yb-s.GroundHeight(b[0], b[2])
	}
	return p
}

// Height is the network reference height at parameter t, identical to terrain.ts profileHeight() (rules
// v4): surveyed deck, engineered span, linear deck between two profiled ends, otherwise the terrain with
// the end offsets tapered along the segment.
func (p *SegmentProfile) Height(t float64) float64 {
	s, a, b := p.terrain, p.a, p.b
	u := clamp(t, 0.0, 1.0)
	x, z := a[0]*(1-u)+b[0]*u, a[2]*(1-u)+b[2]*u
	if p.legacy {
		return s.RoadHeight(x, z, a[1]*(1-u)+b[1]*u)
	}
	// Absorbed into a breakline patch: draped on the surface it shaped, zero offset.
	if p.integrated {
		return s.GroundHeight(x, z) - streetAboveRoad
	}
	// A span carries the network reference itself, exactly as drawn (UI cityMesh surfaceAt). Vehicle
	// offsets above it (rail head, ride height) belong to the vehicle contracts (RailContract).
	if p.pieces != nil {
		at := u
		if p.reversed {
			at = 1 - u
		}
		if h, ok := spanAt(p.pieces, at); ok {
			return h
		}
	}
	if p.linear {
		return p.ya*(1-u) + p.yb*u
	}
	return s.GroundHeight(x, z) + p.offA*(1-u) + p.offB*u
}

// ProfileHeight is the network reference height between two network points [x, legacyY, z].
func (s *TerrainService) ProfileHeight(a, b []float64, t float64) float64 {
	return s.Segment(a, b).Height(t)
}

// ProfileHeightAbsolute interpolates two surveyed RH2000 heights (metres) along a segment.
func (s *TerrainService) ProfileHeightAbsolute(t, fromMetres, toMetres float64) float64 {
	u := clamp(t, 0.0, 1.0)
	return s.ToSceneHeight(fromMetres*(1-u) + toMetres*u)
}

func mustDecodeResource(name string, target any) {
	raw, err := resources.FS.ReadFile(name)
	if err == nil {
		err = json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF")), target)
	}
	if err != nil {
		panic(fmt.Errorf("bundled %s: %w", name, err))
	}
}

// BundledTerrain is the terrain shipped in the binary. It is decoded once, on first use.
var BundledTerrain = sync.OnceValue(func() *TerrainService {
	var data TerrainDataset
	mustDecodeResource("terrain.json", &data)
	service, err := NewTerrainService(&data)
	if err != nil {
		panic(errors.Join(errors.New("bundled terrain.json"), err))
	}
	return service
})
