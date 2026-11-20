package tibi.android

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

/** Server URL and API token, private to the app. */
class Settings(ctx: Context) {
    private val p = ctx.getSharedPreferences("tibi", Context.MODE_PRIVATE)
    var server: String
        get() = p.getString("server", "") ?: ""
        set(v) = p.edit().putString("server", v.trim().trimEnd('/')).apply()
    var token: String
        get() = p.getString("token", "") ?: ""
        set(v) = p.edit().putString("token", v.trim()).apply()
    val configured get() = server.isNotEmpty() && token.isNotEmpty()
    fun api() = Api(server, token)

    /** The last links handed out, newest first: (url, label). */
    fun recent(): List<Pair<String, String>> {
        val a = runCatching { JSONArray(p.getString("recent", "[]")) }.getOrElse { JSONArray() }
        return (0 until a.length()).map { a.getJSONObject(it).let { o -> o.getString("url") to o.optString("label") } }
    }

    fun addRecent(url: String, label: String) {
        val list = listOf(url to label) + recent().filter { it.first != url }
        val a = JSONArray()
        list.take(MAX_RECENT).forEach { (u, l) -> a.put(JSONObject().put("url", u).put("label", l)) }
        p.edit().putString("recent", a.toString()).apply()
    }

    companion object {
        const val MAX_RECENT = 20
    }
}
