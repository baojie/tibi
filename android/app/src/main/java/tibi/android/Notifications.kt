package tibi.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat

object Notifications {
    private const val PROGRESS = "progress"
    private const val RESULT = "result"

    fun channels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(PROGRESS, ctx.getString(R.string.channel_progress), NotificationManager.IMPORTANCE_LOW))
        nm.createNotificationChannel(NotificationChannel(RESULT, ctx.getString(R.string.channel_result), NotificationManager.IMPORTANCE_DEFAULT))
    }

    fun progressId(job: String) = job.hashCode()
    private fun resultId(job: String) = job.hashCode() + 1

    fun progress(ctx: Context, job: String, sent: Long, total: Long): Notification {
        channels(ctx)
        val b = NotificationCompat.Builder(ctx, PROGRESS)
            .setSmallIcon(R.drawable.ic_upload)
            .setContentTitle(ctx.getString(R.string.uploading))
            .setOngoing(true)
            .setOnlyAlertOnce(true)
        if (total > 0) {
            b.setProgress(1000, (sent * 1000 / total).toInt(), false)
            b.setContentText("${mb(sent)} / ${mb(total)}")
        } else {
            b.setProgress(0, 0, true)
        }
        return b.build()
    }

    private fun mb(n: Long) = String.format("%.1f MB", n / 1048576.0)

    private fun post(ctx: Context, id: Int, n: Notification) {
        runCatching { NotificationManagerCompat.from(ctx).notify(id, n) } // no permission: silently skip
    }

    fun update(ctx: Context, job: String, sent: Long, total: Long) = post(ctx, progressId(job), progress(ctx, job, sent, total))

    fun waiting(ctx: Context, job: String) {
        channels(ctx)
        post(ctx, progressId(job), NotificationCompat.Builder(ctx, PROGRESS)
            .setSmallIcon(R.drawable.ic_upload)
            .setContentTitle(ctx.getString(R.string.waiting_network))
            .setOnlyAlertOnce(true)
            .build())
    }

    fun done(ctx: Context, job: String, url: String, count: Int) {
        channels(ctx)
        NotificationManagerCompat.from(ctx).cancel(progressId(job))
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val open = PendingIntent.getActivity(ctx, resultId(job), Intent(Intent.ACTION_VIEW, Uri.parse(url)), flags)
        val copy = PendingIntent.getBroadcast(ctx, resultId(job),
            Intent(ctx, ActionReceiver::class.java).setAction(ActionReceiver.COPY).putExtra("url", url), flags)
        val share = PendingIntent.getActivity(ctx, resultId(job) + 1,
            Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, url), null), flags)
        val title = if (count > 1) ctx.getString(R.string.copied_collection, count) else ctx.getString(R.string.copied)
        post(ctx, resultId(job), NotificationCompat.Builder(ctx, RESULT)
            .setSmallIcon(R.drawable.ic_upload)
            .setContentTitle(title)
            .setContentText(url)
            .setContentIntent(open)
            .setAutoCancel(true)
            .addAction(0, ctx.getString(R.string.copy), copy)
            .addAction(0, ctx.getString(R.string.share), share)
            .build())
    }

    fun failed(ctx: Context, job: String, message: String) {
        channels(ctx)
        NotificationManagerCompat.from(ctx).cancel(progressId(job))
        post(ctx, resultId(job), NotificationCompat.Builder(ctx, RESULT)
            .setSmallIcon(R.drawable.ic_upload)
            .setContentTitle(ctx.getString(R.string.failed))
            .setContentText(message)
            .setStyle(NotificationCompat.BigTextStyle().bigText(message))
            .build())
    }
}
