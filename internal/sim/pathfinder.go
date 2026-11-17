package sim

import "math"

// Horizontal flight lanes, spherical exclusions, and a bounded visibility-checked A* grid.

const (
	pathStep  = 4.0
	pathBound = 20
)

type pathCell struct{ x, z int }

// PathClear reports whether the segment a-b keeps its clearance to every zone.
func PathClear(a, b Vec3, zones []ZoneView) bool {
	for _, zone := range zones {
		c := vecOf(zone.Center)
		ab := b.Sub(a)
		t := 0.0
		if ab.Dot(ab) >= 1e-8 {
			t = clamp(c.Sub(a).Dot(ab)/ab.Dot(ab), 0, 1)
		}
		if a.Add(ab.Mul(t)).Sub(c).Length() < zone.Radius+2.0 {
			return false
		}
	}
	return true
}

// PathRoute returns the waypoints from start to end around the zones, or nothing when there is no route.
func PathRoute(start, end Vec3, zones []ZoneView) []Vec3 {
	if PathClear(start, end, zones) {
		return []Vec3{end}
	}
	point := func(c pathCell) Vec3 { return Vec3{float64(c.x) * pathStep, start.Y, float64(c.z) * pathStep} }
	anchor := func(p Vec3) (pathCell, bool) {
		best, found, bestDistance := pathCell{}, false, 0.0
		for x := -pathBound; x <= pathBound; x++ {
			for z := -pathBound; z <= pathBound; z++ {
				c := pathCell{x, z}
				if !PathClear(p, point(c), zones) {
					continue
				}
				if d := point(c).Sub(p).Length(); !found || d < bestDistance {
					best, found, bestDistance = c, true, d
				}
			}
		}
		return best, found
	}
	from, ok := anchor(start)
	if !ok {
		return nil
	}
	to, ok := anchor(end)
	if !ok {
		return nil
	}
	type candidate struct {
		cell  pathCell
		score float64
	}
	queue := javaPQ[candidate]{less: func(a, b candidate) bool { return a.score < b.score }}
	costs := map[pathCell]float64{from: 0}
	parents := map[pathCell]pathCell{}
	closed := map[pathCell]bool{}
	queue.Add(candidate{from, 0})
	for queue.Len() > 0 {
		current := queue.Remove().cell
		if closed[current] {
			continue
		}
		closed[current] = true
		if current == to {
			points := []Vec3{end, point(to)}
			for cursor := to; cursor != from; {
				cursor = parents[cursor]
				points = append(points, point(cursor))
			}
			for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
				points[i], points[j] = points[j], points[i]
			}
			var smooth []Vec3
			previous := start
			for index := 0; index < len(points); {
				furthest := len(points) - 1
				for furthest > index && !PathClear(previous, points[furthest], zones) {
					furthest--
				}
				previous = points[furthest]
				smooth = append(smooth, previous)
				index = furthest + 1
			}
			return smooth
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				if dx == 0 && dz == 0 {
					continue
				}
				next := pathCell{current.x + dx, current.z + dz}
				if next.x > pathBound || next.x < -pathBound || next.z > pathBound || next.z < -pathBound ||
					closed[next] || !PathClear(point(current), point(next), zones) {
					continue
				}
				score := costs[current] + math.Hypot(float64(dx), float64(dz))*pathStep
				if known, seen := costs[next]; !seen || score < known {
					costs[next] = score
					parents[next] = current
					queue.Add(candidate{next, score + point(next).Sub(end).Length()})
				}
			}
		}
	}
	return nil
}
