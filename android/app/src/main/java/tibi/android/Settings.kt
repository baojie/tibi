package tibi.android

import android.content.Context

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
}
