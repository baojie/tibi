package tibi.android

import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import android.util.Log
import androidx.work.CoroutineWorker
import androidx.work.ForegroundInfo
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

/**
 * Uploads one job. Every file goes up with tus; progress is saved after
 * each step, so a retry (after losing the network, a reboot, …) continues
 * from the bytes the server already has. Several files become a collection.
 */
class UploadWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    private val jobId get() = inputData.getString("job") ?: ""

    override suspend fun getForegroundInfo(): ForegroundInfo = foreground(0, 0)

    private fun foreground(sent: Long, total: Long): ForegroundInfo {
        val n = Notifications.progress(applicationContext, jobId, sent, total)
        return if (Build.VERSION.SDK_INT >= 29) {
            ForegroundInfo(Notifications.progressId(jobId), n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(Notifications.progressId(jobId), n)
        }
    }

    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        val ctx = applicationContext
        val job = Job.load(ctx, jobId) ?: return@withContext Result.failure()
        val settings = Settings(ctx)
        if (!settings.configured) {
            Notifications.failed(ctx, jobId, ctx.getString(R.string.not_configured))
            return@withContext Result.failure()
        }
        // Long uploads run as a foreground service (not allowed from the
        // background on Android 12+, e.g. on a retry; then just carry on).
        runCatching { setForeground(foreground(0, 0)) }
        val api = settings.api()
        val total = job.entries.sumOf { if (it.itemId == null) job.file(it).length() else 0L }
        var before = 0L
        var lastShown = 0L
        try {
            for (e in job.entries) {
                if (e.itemId != null) continue
                val f = job.file(e)
                var loc = e.location
                var off = 0L
                if (loc != null) {
                    val o = api.offset(loc)
                    if (o == null) loc = null else off = o
                }
                if (loc == null) {
                    loc = api.create(f.length(), UploadOptions(filename = e.name.ifEmpty { null }, title = e.title, kind = e.kind))
                    e.location = loc
                    job.save()
                }
                Log.i(TAG, "job $jobId: ${e.name} from $off of ${f.length()}")
                api.patch(loc, f, off) { sent, _ ->
                    val now = System.currentTimeMillis()
                    if (now - lastShown > 300) {
                        lastShown = now
                        Notifications.update(ctx, jobId, before + sent, total)
                        setProgressAsync(workDataOf("sent" to before + sent, "total" to total))
                    }
                }
                before += f.length()
                val item = api.uploadItem(loc)
                e.itemId = item.id
                e.itemUrl = item.url
                job.save()
                f.delete()
            }
            if (job.entries.size > 1 && job.collectionUrl == null) {
                job.collectionUrl = api.createCollection(job.entries.map { it.itemId!! }, null)
                job.save()
            }
            val url = job.resultUrl!!
            Uploads.copy(ctx, url)
            val label = if (job.entries.size > 1) ctx.getString(R.string.items, job.entries.size)
            else job.entries[0].name.ifEmpty { job.entries[0].title ?: "text" }
            settings.addRecent(url, label)
            Notifications.done(ctx, jobId, url, job.entries.size)
            Log.i(TAG, "job $jobId done: $url")
            job.delete()
            Result.success(workDataOf("url" to url, "count" to job.entries.size))
        } catch (e: ApiException) {
            Log.w(TAG, "job $jobId: ${e.status} ${e.message}")
            if (e.retryable && runAttemptCount < 8) return@withContext Result.retry()
            Notifications.failed(ctx, jobId, e.message ?: "HTTP ${e.status}")
            job.delete()
            Result.failure(workDataOf("error" to (e.message ?: "HTTP ${e.status}")))
        } catch (e: IOException) {
            // Network trouble: retry when connected (saved progress kept).
            Log.w(TAG, "job $jobId: ${e.javaClass.simpleName}: ${e.message}; will retry")
            Notifications.waiting(ctx, jobId)
            Result.retry()
        }
    }

    companion object {
        const val TAG = "tibi"
    }
}
