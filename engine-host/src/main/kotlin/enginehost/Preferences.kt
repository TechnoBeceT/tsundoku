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

    private sealed interface StoredPreference {
        data object Missing : StoredPreference

        data class StringValue(
            val value: String,
        ) : StoredPreference

        data class BooleanValue(
            val value: Boolean,
        ) : StoredPreference

        data class IntValue(
            val value: Int,
        ) : StoredPreference

        data class FloatValue(
            val value: Float,
        ) : StoredPreference

        data class LongValue(
            val value: Long,
        ) : StoredPreference

        data class StringSetValue(
            val value: Set<String>,
        ) : StoredPreference
    }

    private fun snapshotAffected(
        preferences: SharedPreferences,
        byKey: Map<String, Preference>,
        changedKeys: Set<String>,
    ): Map<String, StoredPreference> {
        return changedKeys
            .filter { key -> byKey[key]?.isEnabled == true }
            .associateWith { key -> snapshot(preferences, requireNotNull(byKey[key])) }
    }

    private fun snapshot(
        preferences: SharedPreferences,
        preference: Preference,
    ): StoredPreference {
        val key = preference.key
        return when (preference.defaultValueType) {
            "String" ->
                preferences.getString(key, null)
                    ?.let(StoredPreference::StringValue)
                    ?: StoredPreference.Missing
            "Boolean" ->
                if (preferences.contains(key)) {
                    StoredPreference.BooleanValue(preferences.getBoolean(key, false))
                } else {
                    StoredPreference.Missing
                }
            "Int", "Integer" ->
                if (preferences.contains(key)) {
                    StoredPreference.IntValue(preferences.getInt(key, 0))
                } else {
                    StoredPreference.Missing
                }
            "Float" ->
                if (preferences.contains(key)) {
                    StoredPreference.FloatValue(preferences.getFloat(key, 0F))
                } else {
                    StoredPreference.Missing
                }
            "Long" ->
                if (preferences.contains(key)) {
                    StoredPreference.LongValue(preferences.getLong(key, 0L))
                } else {
                    StoredPreference.Missing
                }
            "Set<String>" ->
                preferences.getStringSet(key, null)
                    ?.toSet()
                    ?.let(StoredPreference::StringSetValue)
                    ?: StoredPreference.Missing
            else -> throw IllegalArgumentException("unsupported preference type ${preference.defaultValueType} for '$key'")
        }
    }

    private fun restore(
        preferences: SharedPreferences,
        snapshot: Map<String, StoredPreference>,
    ) {
        val editor = preferences.edit()
        snapshot.forEach { (key, stored) ->
            when (stored) {
                StoredPreference.Missing -> editor.remove(key)
                is StoredPreference.StringValue -> editor.putString(key, stored.value)
                is StoredPreference.BooleanValue -> editor.putBoolean(key, stored.value)
                is StoredPreference.IntValue -> editor.putInt(key, stored.value)
                is StoredPreference.FloatValue -> editor.putFloat(key, stored.value)
                is StoredPreference.LongValue -> editor.putLong(key, stored.value)
                is StoredPreference.StringSetValue -> editor.putStringSet(key, stored.value)
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
