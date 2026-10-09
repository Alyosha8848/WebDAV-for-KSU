package com.dufsbox.app.data

import org.json.JSONArray
import org.json.JSONObject

/**
 * Defensive `org.json` helpers. Every accessor tolerates a missing key, a `null` value or
 * a value of an unexpected type, so a slightly older/newer daemon can never crash the app.
 */

fun JSONObject?.obj(key: String): JSONObject? {
    val source = this ?: return null
    val value = source.opt(key) ?: return null
    return when (value) {
        is JSONObject -> value
        is String -> runCatching { JSONObject(value) }.getOrNull()
        else -> null
    }
}

fun JSONObject?.arr(key: String): JSONArray {
    val source = this ?: return JSONArray()
    val value = source.opt(key) ?: return JSONArray()
    return when (value) {
        is JSONArray -> value
        is JSONObject -> JSONArray().put(value)
        else -> JSONArray()
    }
}

fun JSONObject?.str(key: String, fallback: String = ""): String {
    val source = this ?: return fallback
    if (source.isNull(key)) return fallback
    val value = source.opt(key) ?: return fallback
    if (value is JSONObject || value is JSONArray) return fallback
    val text = value.toString()
    return if (text == "null") fallback else text
}

fun JSONObject?.strOrNull(key: String): String? =
    str(key, "").takeIf { it.isNotBlank() }

fun JSONObject?.long(key: String, fallback: Long = 0L): Long {
    val source = this ?: return fallback
    val value = source.opt(key) ?: return fallback
    return when (value) {
        is Number -> value.toLong()
        is String -> value.trim().toLongOrNull() ?: fallback
        is Boolean -> if (value) 1L else 0L
        else -> fallback
    }
}

fun JSONObject?.int(key: String, fallback: Int = 0): Int {
    val source = this ?: return fallback
    val value = source.opt(key) ?: return fallback
    return when (value) {
        is Number -> value.toInt()
        is String -> value.trim().toIntOrNull() ?: fallback
        is Boolean -> if (value) 1 else 0
        else -> fallback
    }
}

fun JSONObject?.bool(key: String, fallback: Boolean = false): Boolean {
    val source = this ?: return fallback
    val value = source.opt(key) ?: return fallback
    return when (value) {
        is Boolean -> value
        is Number -> value.toInt() != 0
        is String -> when (value.trim().lowercase()) {
            "true", "1", "yes", "on" -> true
            "false", "0", "no", "off", "" -> false
            else -> fallback
        }

        else -> fallback
    }
}

/** Objects contained in `data` (the daemon wraps every successful reply in `data`). */
fun JSONObject?.data(): JSONObject? = obj("data")

/** Objects contained in `data.<key>`. */
fun JSONObject?.dataAt(key: String): JSONObject? = data().obj(key)

/** Arrays contained in `data.<key>`. */
fun JSONObject?.dataArray(key: String): JSONArray = data().arr(key)

fun JSONObject?.stringList(key: String): List<String> {
    val array = arr(key)
    if (array.length() == 0) return emptyList()
    val out = ArrayList<String>(array.length())
    for (i in 0 until array.length()) {
        val value = array.opt(i) ?: continue
        val text = if (value is JSONObject || value is JSONArray) continue else value.toString()
        if (text.isNotBlank() && text != "null") out += text
    }
    return out
}

/** `data.<key>` as a list of strings. */
fun JSONObject?.dataStringList(key: String): List<String> = data().stringList(key)
