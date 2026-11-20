package tibi.android

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.UUID

/** One file of a job, and how far its upload got. */
data class Entry(
    val file: String,
    val name: String,
    val mime: String,
    val kind: String? = null,
    val title: String? = null,
    var location: String? = null,
    var itemId: String? = null,
    var itemUrl: String? = null,
)

/**
 * A share waiting to be uploaded: its files are copied into the app's
 * storage (share permissions do not outlive the share), and its progress is
 * saved after every step, so the worker can stop and continue at any time.
 */
class Job(val dir: File, val entries: List<Entry>, var collectionUrl: String? = null) {
    val id: String get() = dir.name
    fun file(e: Entry) = File(dir, e.file)

    /** The link to hand out: the collection, or the single item. */
    val resultUrl: String? get() = collectionUrl ?: entries.singleOrNull()?.itemUrl

    fun save() {
        val arr = JSONArray()
        for (e in entries) arr.put(
            JSONObject().put("file", e.file).put("name", e.name).put("mime", e.mime)
                .putOpt("kind", e.kind).putOpt("title", e.title).putOpt("location", e.location)
                .putOpt("item_id", e.itemId).putOpt("item_url", e.itemUrl)
        )
        val tmp = File(dir, "job.json.tmp")
        tmp.writeText(JSONObject().put("entries", arr).putOpt("collection_url", collectionUrl).toString())
        tmp.renameTo(File(dir, "job.json"))
    }

    fun delete() = dir.deleteRecursively()

    companion object {
        fun root(ctx: Context) = File(ctx.filesDir, "outbox").apply { mkdirs() }

        fun newDir(ctx: Context): File = File(root(ctx), UUID.randomUUID().toString()).apply { mkdirs() }

        fun load(ctx: Context, id: String): Job? {
            val dir = File(root(ctx), id)
            val f = File(dir, "job.json")
            if (!f.exists()) return null
            val j = JSONObject(f.readText())
            val arr = j.getJSONArray("entries")
            val entries = (0 until arr.length()).map {
                val e = arr.getJSONObject(it)
                Entry(
                    e.getString("file"), e.getString("name"), e.getString("mime"),
                    e.optString("kind").ifEmpty { null }, e.optString("title").ifEmpty { null },
                    e.optString("location").ifEmpty { null },
                    e.optString("item_id").ifEmpty { null }, e.optString("item_url").ifEmpty { null },
                )
            }
            return Job(dir, entries, j.optString("collection_url").ifEmpty { null })
        }
    }
}
