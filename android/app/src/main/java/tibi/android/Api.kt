package tibi.android

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.util.Base64
import java.util.concurrent.TimeUnit

/** An error answer from tibi (4xx/5xx). [retryable] for 5xx and 429. */
class ApiException(val status: Int, message: String) : IOException(message) {
    val retryable get() = status >= 500 || status == 429
}

/** Upload options, as in the API (`/api/v1/items` query / tus metadata). */
data class UploadOptions(
    val filename: String? = null,
    val title: String? = null,
    val kind: String? = null,
    val source: String = "android",
    val sourceUrl: String? = null,
) {
    fun values(): Map<String, String> = buildMap {
        filename?.let { put("filename", it) }
        title?.let { put("title", it) }
        kind?.let { put("kind", it) }
        put("source", source)
        sourceUrl?.let { put("source_url", it) }
    }
}

private val urlRe = Regex("""https?://[^\s<>"]+""", RegexOption.IGNORE_CASE)

/**
 * The first web address in shared text (browsers put the page's address
 * there when sharing a page or an image), as the API accepts it for
 * `source_url`: http(s), at most 2048 bytes. Trailing punctuation is not
 * part of it.
 */
fun webUrl(text: String?): String? {
    val m = urlRe.find(text ?: return null) ?: return null
    val u = m.value.trimEnd('.', ',', ';', ':', '!', '?', ')', ']', '}', '\'', '"', '。', '，', '）')
    return u.takeIf { it.toByteArray().size <= 2048 && it.substringAfter("://").isNotEmpty() }
}

data class Item(val id: String, val url: String, val kind: String)

/**
 * The parts of the tibi API the app uses. All uploads go through tus
 * (`/api/v1/uploads/`), so an interrupted one continues where it stopped.
 */
class Api(server: String, private val token: String, private val http: OkHttpClient = defaultClient) {
    val server = server.trimEnd('/')

    companion object {
        const val CHUNK = 4L shl 20
        val defaultClient: OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .writeTimeout(60, TimeUnit.SECONDS)
            .readTimeout(120, TimeUnit.SECONDS) // the last PATCH waits for ingest (HEIC conversion…)
            .build()
    }

    private fun req(url: String) = Request.Builder().url(url)
        .header("Authorization", "Bearer $token")
        .header("Tus-Resumable", "1.0.0")

    private fun call(r: Request): Response {
        val resp = http.newCall(r).execute()
        if (!resp.isSuccessful) {
            val body = resp.body.string()
            resp.close()
            val msg = runCatching { JSONObject(body).getJSONObject("error").getString("message") }
                .getOrElse { body.trim().removePrefix("ERR_").substringAfter(": ").ifEmpty { "HTTP ${resp.code}" } }
            throw ApiException(resp.code, msg)
        }
        return resp
    }

    /** The token's name (connection test). */
    fun whoami(): String = call(req("$server/api/v1/whoami").get().build()).use {
        JSONObject(it.body.string()).optString("name")
    }

    /** Starts a tus upload; returns its URL. */
    fun create(size: Long, o: UploadOptions): String {
        val meta = o.values().entries.joinToString(",") { (k, v) ->
            k + " " + Base64.getEncoder().encodeToString(v.toByteArray())
        }
        val r = req("$server/api/v1/uploads/")
            .header("Upload-Length", size.toString())
            .header("Upload-Metadata", meta)
            .post(ByteArray(0).toRequestBody())
            .build()
        return call(r).use {
            val loc = it.header("Location") ?: throw IOException("no upload URL")
            if (loc.startsWith("/")) server + loc else loc
        }
    }

    /** How many bytes of the upload the server has; null if it is gone. */
    fun offset(location: String): Long? {
        val resp = http.newCall(req(location).head().build()).execute()
        resp.use {
            if (it.code == 404 || it.code == 410) return null
            if (!it.isSuccessful) throw ApiException(it.code, "HTTP ${it.code}")
            return it.header("Upload-Offset")?.toLongOrNull()
        }
    }

    /** Sends the file from [offset] on in chunks; returns the final offset. */
    fun patch(location: String, file: File, offset: Long, progress: (Long, Long) -> Unit): Long {
        var off = offset
        val size = file.length()
        while (off < size) {
            val n = minOf(CHUNK, size - off)
            val start = off
            val body = object : RequestBody() {
                override fun contentType() = "application/offset+octet-stream".toMediaType()
                override fun contentLength() = n
                override fun writeTo(sink: BufferedSink) {
                    RandomAccessFile(file, "r").use { raf ->
                        raf.seek(start)
                        val buf = ByteArray(64 shl 10)
                        var left = n
                        while (left > 0) {
                            val k = raf.read(buf, 0, minOf(buf.size.toLong(), left).toInt())
                            if (k < 0) throw IOException("file shrank")
                            sink.write(buf, 0, k)
                            left -= k
                        }
                    }
                }
            }
            val r = req(location).header("Upload-Offset", off.toString()).patch(body).build()
            off = call(r).use { it.header("Upload-Offset")?.toLongOrNull() ?: throw IOException("no Upload-Offset") }
            progress(off, size)
        }
        return off
    }

    /** The item a finished upload became. */
    fun uploadItem(location: String): Item =
        call(req(location.trimEnd('/') + "/item").get().build()).use { item(JSONObject(it.body.string())) }

    /** Gathers items into a new collection; returns its URL. */
    fun createCollection(ids: List<String>, title: String?): String {
        val body = JSONObject().put("items", JSONArray(ids))
        if (!title.isNullOrEmpty()) body.put("title", title)
        val r = req("$server/api/v1/collections")
            .post(body.toString().toRequestBody("application/json".toMediaType())).build()
        return call(r).use { JSONObject(it.body.string()).getString("url") }
    }

    private fun item(j: JSONObject) = Item(j.getString("id"), j.getString("url"), j.getString("kind"))
}
