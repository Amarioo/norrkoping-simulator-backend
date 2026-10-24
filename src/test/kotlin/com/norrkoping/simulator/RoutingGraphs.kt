package com.norrkoping.simulator

/** Test helper: nodes of the largest strongly connected component of [allowed] (iterative Kosaraju). */
internal fun largestStrongComponent(n: TransportNetwork, allowed: Collection<Int>): Set<Int> {
    val size = n.nodes.size
    val forward = Array(size) { mutableListOf<Int>() }
    val backward = Array(size) { mutableListOf<Int>() }
    for (e in allowed) { forward[n.edges[e].from].add(n.edges[e].to); backward[n.edges[e].to].add(n.edges[e].from) }
    val seen = BooleanArray(size)
    val order = ArrayList<Int>()
    for (start in 0 until size) {
        if (seen[start] || forward[start].isEmpty()) continue
        seen[start] = true
        val stack = ArrayDeque(listOf(start to 0))
        while (stack.isNotEmpty()) {
            val (u, i) = stack.removeLast()
            if (i < forward[u].size) {
                stack.addLast(u to i + 1)
                val v = forward[u][i]
                if (!seen[v]) { seen[v] = true; stack.addLast(v to 0) }
            } else order.add(u)
        }
    }
    val component = IntArray(size) { -1 }
    val counts = mutableListOf<Int>()
    for (start in order.asReversed()) {
        if (component[start] >= 0) continue
        val id = counts.size; var count = 0
        component[start] = id
        val stack = ArrayDeque(listOf(start))
        while (stack.isNotEmpty()) {
            val u = stack.removeLast(); count++
            for (v in backward[u]) if (component[v] < 0) { component[v] = id; stack.addLast(v) }
        }
        counts.add(count)
    }
    val main = counts.indices.maxByOrNull { counts[it] } ?: return emptySet()
    return (0 until size).filterTo(HashSet()) { component[it] == main }
}
