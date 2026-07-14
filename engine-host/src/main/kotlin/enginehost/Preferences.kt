package enginehost

/*
 * Portions adapted from Suwayomi-Server (Mozilla Public License 2.0):
 *   suwayomi.tachidesk.manga.impl.Source.getSourcePreferencesRaw / setSourcePreference
 * The DB coupling is removed — this reads a ConfigurableSource's preference descriptors and
 * writes new values straight to the source's SharedPreferences (persisted on the volume by
 * AndroidCompat), so a preference change survives a restart and is picked up on source reload.
 *
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

import androidx.preference.ListPreference
import androidx.preference.MultiSelectListPreference
import androidx.preference.Preference
import androidx.preference.PreferenceScreen
import eu.kanade.tachiyomi.source.ConfigurableSource
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.sourcePreferences
import io.github.oshai.kotlinlogging.KotlinLogging
import uy.kohesive.injekt.injectLazy
import xyz.nulldev.androidcompat.androidimpl.CustomContext

/**
 * Preferences extracts per-source preference descriptors from a [ConfigurableSource] and writes
 * new values to its SharedPreferences. Sources that aren't configurable yield an empty list.
 */
object Preferences {
    private val logger = KotlinLogging.logger {}
    private val context: CustomContext by injectLazy()

    /** Build the source's PreferenceScreen and return the raw androidx.preference entries. */
    private fun rawPreferences(source: Source): List<Preference> {
        if (source !is ConfigurableSource) return emptyList()
        val screen = PreferenceScreen(context)
        screen.sharedPreferences = source.sourcePreferences()
        source.setupPreferenceScreen(screen)
        return screen.preferences
    }

    /** Describe every preference of a source (empty for a non-configurable source). */
    fun describe(source: Source): List<PreferenceDto> =
        rawPreferences(source).map { pref ->
            PreferenceDto(
                key = pref.key,
                type = pref::class.java.simpleName,
                title = pref.title?.toString(),
                summary = pref.summary?.toString(),
                currentValue = pref.currentValue,
                defaultValue = pref.defaultValue,
                entries = (pref as? ListPreference)?.entries?.map { it.toString() }
                    ?: (pref as? MultiSelectListPreference)?.entries?.map { it.toString() },
                entryValues = (pref as? ListPreference)?.entryValues?.map { it.toString() }
                    ?: (pref as? MultiSelectListPreference)?.entryValues?.map { it.toString() },
            )
        }

    /**
     * Apply a batch of preference changes to a source (persisted to its SharedPreferences).
     * Each value is coerced to the preference's declared type. Returns the keys actually written.
     * The caller is responsible for reloading the source so a construction-time-cached pref is
     * re-read (see [ExtensionManager.reloadForSource]).
     */
    fun apply(
        source: Source,
        changes: Map<String, Any?>,
    ): List<String> {
        val byKey = rawPreferences(source).associateBy { it.key }
        val written = mutableListOf<String>()
        changes.forEach { (key, raw) ->
            val pref = byKey[key] ?: throw IllegalArgumentException("unknown preference key '$key' for source ${source.name}")
            if (!pref.isEnabled) {
                logger.warn { "preference '$key' is disabled, skipping" }
                return@forEach
            }
            val coerced = coerce(pref, raw)
            pref.saveNewValue(coerced)
            pref.callChangeListener(coerced)
            written += key
        }
        return written
    }

    /** Coerce a JSON-decoded value to the exact type the preference persists. */
    private fun coerce(
        pref: Preference,
        raw: Any?,
    ): Any =
        when (pref.defaultValueType) {
            "String" -> raw?.toString() ?: ""
            "Boolean" -> when (raw) {
                is Boolean -> raw
                is String -> raw.toBoolean()
                else -> throw IllegalArgumentException("preference '${pref.key}' expects a boolean, got $raw")
            }
            "Set<String>" -> when (raw) {
                is Collection<*> -> raw.map { it.toString() }.toSet()
                else -> throw IllegalArgumentException("preference '${pref.key}' expects a string array, got $raw")
            }
            else -> throw IllegalArgumentException("unsupported preference type ${pref.defaultValueType} for '${pref.key}'")
        }
}
