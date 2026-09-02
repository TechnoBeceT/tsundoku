package enginehost

import eu.kanade.tachiyomi.source.Source

/** Publish unowned source fixtures into an immutable registry snapshot. */
internal fun ExtensionLoader.publishTestSources(replacement: Collection<Source>) {
    val current = snapshotRegistry()
    val nextSources = HashMap(current.sources)
    replacement.forEach { nextSources[it.id] = it }
    publishRegistry(prepareRegistry(nextSources, current.installed))
}
