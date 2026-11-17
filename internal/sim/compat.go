package sim

import (
	"math"
	"strconv"
	"strings"
)

// This file holds the pieces of the Kotlin/JVM runtime that the simulation's results depend on. The Go
// port reproduces the Kotlin implementation's trajectories exactly (testdata/golden.json), which needs
// the same random numbers, the same priority-queue tie-breaking and the same number formatting.

// KRandom is kotlin.random.Random(seed): the XorWow generator of the Kotlin standard library.
type KRandom struct{ x, y, z, w, v, addend int32 }

func NewKRandom(seed int32) *KRandom {
	seed2 := seed >> 31
	r := &KRandom{x: seed, y: seed2, v: ^seed, addend: (seed << 10) ^ int32(uint32(seed2)>>4)}
	for i := 0; i < 64; i++ {
		r.NextInt()
	}
	return r
}

func (r *KRandom) NextInt() int32 {
	t := r.x
	t ^= int32(uint32(t) >> 2)
	r.x, r.y, r.z = r.y, r.z, r.w
	v0 := r.v
	r.w = v0
	t = (t ^ (t << 1)) ^ v0 ^ (v0 << 4)
	r.v = t
	r.addend += 362437
	return t + r.addend
}

func (r *KRandom) nextBits(bits int) int32 {
	if bits == 0 {
		r.NextInt()
		return 0
	}
	return int32(uint32(r.NextInt()) >> (32 - bits))
}

// NextDouble is uniform in [0, 1).
func (r *KRandom) NextDouble() float64 {
	hi := int64(r.nextBits(26))
	lo := int64(r.nextBits(27))
	return float64(hi<<27+lo) / float64(int64(1)<<53)
}

// NextIntN is uniform in [0, until); until must be positive.
func (r *KRandom) NextIntN(until int) int {
	n := int32(until)
	if n <= 0 {
		panic("NextIntN: bound must be positive")
	}
	if n&-n == n {
		log2 := 0
		for m := n; m > 1; m >>= 1 {
			log2++
		}
		return int(r.nextBits(log2))
	}
	for {
		bits := int32(uint32(r.NextInt()) >> 1)
		v := bits % n
		if bits-v+(n-1) >= 0 {
			return int(v)
		}
	}
}

// NextDoubleRange is uniform in [from, until).
func (r *KRandom) NextDoubleRange(from, until float64) float64 {
	v := from + r.NextDouble()*(until-from)
	if v >= until {
		return math.Nextafter(until, math.Inf(-1))
	}
	return v
}

// javaPQ is java.util.PriorityQueue: the same sift order, so equal priorities leave in the same order.
type javaPQ[T any] struct {
	items []T
	less  func(a, b T) bool
}

func (q *javaPQ[T]) Len() int { return len(q.items) }

func (q *javaPQ[T]) Add(x T) {
	q.items = append(q.items, x)
	k := len(q.items) - 1
	for k > 0 {
		parent := (k - 1) >> 1
		e := q.items[parent]
		if !q.less(x, e) {
			break
		}
		q.items[k] = e
		k = parent
	}
	q.items[k] = x
}

func (q *javaPQ[T]) Remove() T {
	result := q.items[0]
	n := len(q.items) - 1
	x := q.items[n]
	q.items = q.items[:n]
	if n > 0 {
		k, half := 0, n>>1
		for k < half {
			child := 2*k + 1
			c := q.items[child]
			if right := child + 1; right < n && q.less(q.items[right], c) {
				child = right
				c = q.items[child]
			}
			if !q.less(c, x) {
				break
			}
			q.items[k] = c
			k = child
		}
		q.items[k] = x
	}
	return result
}

// javaDouble formats like Kotlin's Double.toString() (string templates): "3.0", "0.25", "1.0E-4".
func javaDouble(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		if math.Signbit(x) {
			return "-0.0"
		}
		return "0.0"
	}
	if a := math.Abs(x); a >= 1e-3 && a < 1e7 {
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(x, 'e', -1, 64), "e")
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	e, _ := strconv.Atoi(exponent)
	return mantissa + "E" + strconv.Itoa(e)
}

// appendJavaFixed appends x like Java's String.format(Locale.ROOT, "%.<decimals>f", x): the shortest
// decimal representation of x, rounded half up. Go's own %f rounds the exact binary value instead, which
// differs for ties such as 1.0005. The bridge profile tables are keyed by this format.
func appendJavaFixed(dst []byte, x float64, decimals int) []byte {
	if math.Signbit(x) {
		dst = append(dst, '-')
		x = -x
	}
	start := len(dst)
	dst = strconv.AppendFloat(dst, x, 'f', -1, 64)
	point := start
	for point < len(dst) && dst[point] != '.' {
		point++
	}
	if point == len(dst) {
		dst = append(dst, '.')
	}
	have := len(dst) - point - 1
	if have <= decimals {
		for ; have < decimals; have++ {
			dst = append(dst, '0')
		}
		return dst
	}
	roundUp := dst[point+1+decimals] >= '5'
	dst = dst[:point+1+decimals]
	if roundUp {
		i := len(dst) - 1
		for ; i >= start; i-- {
			if dst[i] == '.' {
				continue
			}
			if dst[i] < '9' {
				dst[i]++
				break
			}
			dst[i] = '0'
		}
		if i < start {
			dst = append(dst, 0)
			copy(dst[start+1:], dst[start:])
			dst[start] = '1'
		}
	}
	return dst
}

func javaFixed(x float64, decimals int) string {
	return string(appendJavaFixed(make([]byte, 0, 24), x, decimals))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// floorInt is Kotlin's floor(x).toInt(), which saturates instead of wrapping.
func floorInt(v float64) int {
	f := math.Floor(v)
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int(f)
}
