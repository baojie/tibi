package tibi.android

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.WorkManager
import androidx.work.workDataOf
import java.io.File
import java.util.concurrent.TimeUnit

/** Something to upload: a shared file (content URI) or shared text. */
sealed class Source {
    data class Content(val uri: Uri, val mime: String?) : Source()
    data class Text(val text: String, val title: String?) : Source()
}

object Uploads {
    const val TAG = "upload"

    /** Copies the sources into a new job and queues its upload. */
    fun start(ctx: Context, sources: List<Source>): Job {
        val dir = Job.newDir(ctx)
        val entries = try { copyAll(ctx, dir, sources) } catch (e: Exception) {
            dir.deleteRecursively() // nothing half-copied stays behind
            throw e
        }
        val job = Job(dir, entries)
        job.save()
        enqueue(ctx, job.id)
        return job
    }

    private fun copyAll(ctx: Context, dir: File, sources: List<Source>): List<Entry> =
        sources.mapIndexed { i, s ->
            when (s) {
                is Source.Text -> {
                    File(dir, "$i.txt").writeText(s.text)
                    Entry("$i.txt", "", "text/plain", kind = "text", title = s.title)
                }
                is Source.Content -> {
                    val mime = s.mime ?: ctx.contentResolver.getType(s.uri) ?: "application/octet-stream"
                    val name = displayName(ctx, s.uri) ?: defaultName(i, mime)
                    val f = File(dir, "$i.bin")
                    ctx.contentResolver.openInputStream(s.uri)!!.use { inp -> f.outputStream().use { inp.copyTo(it) } }
                    Entry(f.name, name, mime)
                }
            }
        }

    fun enqueue(ctx: Context, jobId: String) {
        val req = OneTimeWorkRequestBuilder<UploadWorker>()
            .setInputData(workDataOf("job" to jobId))
            // No network (airplane mode…): wait; it continues when back.
            .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 10, TimeUnit.SECONDS)
            .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
            .addTag(TAG)
            .build()
        WorkManager.getInstance(ctx).enqueueUniqueWork(jobId, ExistingWorkPolicy.KEEP, req)
    }

    fun displayName(ctx: Context, uri: Uri): String? = runCatching {
        ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        }
    }.getOrNull()?.takeIf { it.isNotBlank() }

    private fun defaultName(i: Int, mime: String): String {
        val ext = MimeTypeMap.getSingleton().getExtensionFromMimeType(mime) ?: "bin"
        return "shared-${i + 1}.$ext"
    }

    fun copy(ctx: Context, url: String) {
        val cm = ctx.getSystemService(ClipboardManager::class.java)
        cm.setPrimaryClip(ClipData.newPlainText("tibi", url))
    }
}
