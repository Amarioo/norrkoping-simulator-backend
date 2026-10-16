package com.norrkoping.simulator

import java.util.PriorityQueue
import kotlin.math.*

/** Horizontal flight lanes, spherical exclusions, and a bounded visibility-checked A* grid. */
object Pathfinder {
    private const val STEP = 4.0
    private const val BOUND = 20
    private data class Cell(val x: Int, val z: Int)
    private data class Candidate(val cell: Cell, val score: Double)
    fun clear(a: Vec3, b: Vec3, zones: Collection<ZoneView>): Boolean = zones.none { zone ->
        val c = Vec3(zone.center[0], zone.center[1], zone.center[2])
        val ab = b - a
        val t = if (ab.dot(ab) < 1e-8) 0.0 else ((c - a).dot(ab) / ab.dot(ab)).coerceIn(0.0, 1.0)
        (a + ab * t - c).length() < zone.radius + 2.0
    }
    fun route(start: Vec3, end: Vec3, zones: Collection<ZoneView>): List<Vec3> {
        if (clear(start, end, zones)) return listOf(end)
        fun point(c: Cell) = Vec3(c.x * STEP, start.y, c.z * STEP)
        fun anchor(p: Vec3): Cell? = (-BOUND..BOUND).flatMap { x -> (-BOUND..BOUND).map { z -> Cell(x, z) } }
            .filter { clear(p, point(it), zones) }.minByOrNull { (point(it) - p).length() }
        val from = anchor(start) ?: return emptyList()
        val to = anchor(end) ?: return emptyList()
        val queue = PriorityQueue(compareBy<Candidate> { it.score })
        val costs = mutableMapOf(from to 0.0)
        val parents = mutableMapOf<Cell, Cell>()
        val closed = mutableSetOf<Cell>()
        queue.add(Candidate(from, 0.0))
        while (queue.isNotEmpty()) {
            val current = queue.remove().cell
            if (!closed.add(current)) continue
            if (current == to) {
                val points = mutableListOf(end, point(to))
                var cursor = to
                while (cursor != from) { cursor = parents.getValue(cursor); points.add(point(cursor)) }
                points.reverse()
                val smooth = mutableListOf<Vec3>()
                var previous = start
                var index = 0
                while (index < points.size) {
                    var furthest = points.lastIndex
                    while (furthest > index && !clear(previous, points[furthest], zones)) furthest--
                    previous = points[furthest]; smooth.add(previous); index = furthest + 1
                }
                return smooth
            }
            for (dx in -1..1) for (dz in -1..1) {
                if (dx == 0 && dz == 0) continue
                val next = Cell(current.x + dx, current.z + dz)
                if (abs(next.x) > BOUND || abs(next.z) > BOUND || next in closed || !clear(point(current), point(next), zones)) continue
                val score = costs.getValue(current) + hypot(dx.toDouble(), dz.toDouble()) * STEP
                if (score < costs.getOrDefault(next, Double.POSITIVE_INFINITY)) {
                    costs[next] = score; parents[next] = current
                    queue.add(Candidate(next, score + (point(next) - end).length()))
                }
            }
        }
        return emptyList()
    }
}