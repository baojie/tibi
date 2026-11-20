package tibi.android

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.View
import android.widget.Button
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.work.WorkInfo
import androidx.work.WorkManager

/**
 * Share target, shown as a small dialog: copies what was shared into the
 * outbox, queues the upload, shows its progress and then the link, with
 * Open, Copy and Share. Hiding the dialog does not stop anything: the
 * upload goes on in the background and its notification carries the link.
 */
class ShareActivity : Activity() {
    private val main = Handler(Looper.getMainLooper())
    private var jobId: String? = null
    private var stopped = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!Settings(this).configured) {
            Toast.makeText(this, R.string.not_configured, Toast.LENGTH_LONG).show()
            startActivity(Intent(this, SettingsActivity::class.java))
            finish()
            return
        }
        val sources = sources(intent)
        if (sources.isEmpty()) {
            Toast.makeText(this, R.string.nothing_to_upload, Toast.LENGTH_LONG).show()
            finish()
            return
        }
        setContentView(R.layout.share)
        setFinishOnTouchOutside(false)
        findViewById<Button>(R.id.close).setOnClickListener { finish() }
        status(getString(R.string.preparing))
        jobId = savedInstanceState?.getString("job")
        if (jobId != null) {
            watch()
            return
        }
        // Copying needs the share's URI grant, which ends with this
        // activity: copy first (in the background), then watch the upload.
        Thread {
            val r = runCatching { Uploads.start(this, sources) }
            main.post {
                r.onSuccess { jobId = it.id; watch() }.onFailure {
                    Log.w(UploadWorker.TAG, "share failed", it)
                    failed(it.message ?: it.javaClass.simpleName)
                }
            }
        }.start()
    }

    override fun onSaveInstanceState(out: Bundle) {
        super.onSaveInstanceState(out)
        jobId?.let { out.putString("job", it) }
    }

    override fun onDestroy() {
        stopped = true
        main.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    private fun status(text: String) {
        findViewById<TextView>(R.id.status).text = text
    }

    /** Polls the upload's WorkInfo until it ends (WorkManager is thread-safe; get() blocks, so off the UI thread). */
    private fun watch() {
        val id = jobId ?: return
        status(getString(R.string.background_note))
        Thread {
            while (!stopped) {
                val info = runCatching { WorkManager.getInstance(this).getWorkInfosForUniqueWork(id).get().firstOrNull() }.getOrNull()
                if (info != null) {
                    val st = info.state
                    main.post { if (!stopped) show(info) }
                    if (st.isFinished) return@Thread
                }
                Thread.sleep(300)
            }
        }.start()
    }

    private fun show(info: WorkInfo) {
        val bar = findViewById<ProgressBar>(R.id.progress)
        when (info.state) {
            WorkInfo.State.SUCCEEDED -> done(info.outputData.getString("url") ?: return, info.outputData.getInt("count", 1))
            WorkInfo.State.FAILED, WorkInfo.State.CANCELLED -> failed(info.outputData.getString("error") ?: getString(R.string.failed))
            WorkInfo.State.ENQUEUED -> if (info.runAttemptCount > 0) status(getString(R.string.waiting_network))
            else -> {
                val sent = info.progress.getLong("sent", -1)
                val total = info.progress.getLong("total", 0)
                if (sent >= 0 && total > 0) {
                    bar.isIndeterminate = false
                    bar.progress = (sent * 1000 / total).toInt()
                    status(String.format("%.1f / %.1f MB", sent / 1048576.0, total / 1048576.0))
                }
            }
        }
    }

    private fun done(url: String, count: Int) {
        findViewById<TextView>(R.id.title).text = getString(if (count > 1) R.string.done_title_collection else R.string.done_title)
        findViewById<ProgressBar>(R.id.progress).visibility = View.GONE
        findViewById<TextView>(R.id.status).visibility = View.GONE
        findViewById<TextView>(R.id.url).apply { text = url; visibility = View.VISIBLE }
        findViewById<View>(R.id.actions).visibility = View.VISIBLE
        findViewById<Button>(R.id.close).setText(R.string.done)
        findViewById<Button>(R.id.copy).setOnClickListener {
            Uploads.copy(this, url)
            Toast.makeText(this, R.string.copied, Toast.LENGTH_SHORT).show()
        }
        findViewById<Button>(R.id.share).setOnClickListener {
            startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, url), null))
        }
        findViewById<Button>(R.id.open).setOnClickListener { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) }
    }

    private fun failed(message: String) {
        findViewById<TextView>(R.id.title).setText(R.string.failed)
        findViewById<ProgressBar>(R.id.progress).visibility = View.GONE
        status(message)
        findViewById<Button>(R.id.close).setText(R.string.done)
    }

    @Suppress("DEPRECATION")
    private fun streams(i: Intent): List<Uri> = when (i.action) {
        Intent.ACTION_SEND -> listOfNotNull(
            if (Build.VERSION.SDK_INT >= 33) i.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            else i.getParcelableExtra(Intent.EXTRA_STREAM)
        )
        Intent.ACTION_SEND_MULTIPLE ->
            (if (Build.VERSION.SDK_INT >= 33) i.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
            else i.getParcelableArrayListExtra(Intent.EXTRA_STREAM)) ?: emptyList()
        else -> emptyList()
    }

    private fun sources(i: Intent): List<Source> {
        val uris = streams(i)
        if (uris.isNotEmpty()) {
            val single = if (uris.size == 1) i.type?.takeIf { !it.contains('*') } else null
            return uris.map { Source.Content(it, single) }
        }
        val text = i.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()
        if (!text.isNullOrBlank()) return listOf(Source.Text(text, i.getStringExtra(Intent.EXTRA_SUBJECT)))
        return emptyList()
    }
}
