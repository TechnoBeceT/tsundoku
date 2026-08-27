package enginehost

import com.fasterxml.jackson.annotation.JsonProperty

data class EngineSourceStatus(
    @get:JsonProperty("source_id") val sourceId: Long,
    val queued: Int,
    val running: Int,
)

data class ExtensionExecutorSnapshot(
    val running: Boolean,
    val queued: Int,
)

/** Bounded, payload-free runtime evidence returned by `GET /status`. */
data class EngineStatus(
    val ready: Boolean,
    @get:JsonProperty("source_workers") val sourceWorkers: Int,
    @get:JsonProperty("per_source_limit") val perSourceLimit: Int,
    val queued: Int,
    val running: Int,
    @get:JsonProperty("completion_sequence") val completionSequence: Long,
    @get:JsonProperty("oldest_running_millis") val oldestRunningMillis: Long,
    val completed: Long,
    val cancelled: Long,
    @get:JsonProperty("timed_out") val timedOut: Long,
    val rejected: Long,
    @get:JsonProperty("busiest_sources") val busiestSources: List<EngineSourceStatus>,
    @get:JsonProperty("extension_running") val extensionRunning: Boolean,
    @get:JsonProperty("extension_queued") val extensionQueued: Int,
) {
    companion object {
        private const val MAX_BUSY_SOURCES = 10

        fun from(
            ready: Boolean,
            source: SourceSchedulerSnapshot,
            extension: ExtensionExecutorSnapshot,
        ): EngineStatus =
            EngineStatus(
                ready = ready,
                sourceWorkers = source.sourceWorkers,
                perSourceLimit = source.perSourceLimit,
                queued = source.queued,
                running = source.running,
                completionSequence = source.completionSequence,
                oldestRunningMillis = source.oldestRunningMillis,
                completed = source.completed,
                cancelled = source.cancelled,
                timedOut = source.timedOut,
                rejected = source.rejected,
                busiestSources =
                    source.sources
                        .asSequence()
                        .filter { it.running > 0 || it.queued > 0 }
                        .sortedWith(
                            compareByDescending<SourceSchedulerSourceSnapshot> { it.running }
                                .thenByDescending { it.queued }
                                .thenBy { it.sourceId },
                        ).take(MAX_BUSY_SOURCES)
                        .map { EngineSourceStatus(sourceId = it.sourceId, queued = it.queued, running = it.running) }
                        .toList(),
                extensionRunning = extension.running,
                extensionQueued = extension.queued,
            )
    }
}
