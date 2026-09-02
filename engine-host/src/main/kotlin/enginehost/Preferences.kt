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

import android.content.SharedPreferences
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
    ): List<String> = apply(source, changes, rawPreferences(source).associateBy { it.key })

    /**
     * Apply [changes] and run [commit] as one recoverable preference transaction. Any failure while
     * applying, materializing, validating, or describing the refreshed source restores every
     * affected persisted value before the failure escapes to the RPC layer.
     */
    internal fun <T> applyRecoverably(
        source: Source,
        changes: Map<String, Any?>,
        commit: () -> T,
    ): T {
        val byKey = rawPreferences(source).associateBy { it.key }
        val sharedPreferences = (source as? ConfigurableSource)?.sourcePreferences()
        val snapshot =
            if (sharedPreferences == null) {
                emptyMap()
            } else {
                snapshotAffected(sharedPreferences, byKey, changes.keys)
            }
        try {
            apply(source, changes, byKey)
            return commit()
        } catch (failure: Throwable) {
            if (sharedPreferences != null) {
                try {
                    restore(sharedPreferences, snapshot)
                } catch (restoreFailure: Throwable) {
                    failure.addSuppressed(restoreFailure)
                }
            }
            throw failure
        }
    }

    private fun apply(
        source: Source,
        changes: Map<String, Any?>,
        byKey: Map<String, Preference>,
    ): List<String> {
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

    private data class StoredPreference(
        val present: Boolean,
        val value: Any?,
    )

    private fun snapshotAffected(
        preferences: SharedPreferences,
        byKey: Map<String, Preference>,
        changedKeys: Set<String>,
    ): Map<String, StoredPreference> {
        val stored = preferences.all
        return changedKeys
            .filter { key -> byKey[key]?.isEnabled == true }
            .associateWith { key ->
                StoredPreference(
                    present = preferences.contains(key),
                    value = (stored[key] as? Set<*>)?.map { it.toString() }?.toSet() ?: stored[key],
                )
            }
    }

    private fun restore(
        preferences: SharedPreferences,
        snapshot: Map<String, StoredPreference>,
    ) {
        val editor = preferences.edit()
        snapshot.forEach { (key, stored) ->
            if (!stored.present) {
                editor.remove(key)
                return@forEach
            }
            when (val value = stored.value) {
                is String -> editor.putString(key, value)
                is Boolean -> editor.putBoolean(key, value)
                is Set<*> -> editor.putStringSet(key, value.map { it.toString() }.toSet())
                is Int -> editor.putInt(key, value)
                is Long -> editor.putLong(key, value)
                is Float -> editor.putFloat(key, value)
                else -> error("cannot restore preference '$key' with value type ${value?.javaClass?.name}")
            }
        }
        check(editor.commit()) { "failed to restore source preferences" }
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
