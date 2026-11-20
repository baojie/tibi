package tibi.android

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.widget.Toast

/**
 * Share target: copies what was shared into the outbox, queues the upload
 * and gets out of the way. Has no UI of its own beyond a toast.
 */
class ShareActivity : Activity() {
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
        Toast.makeText(this, R.string.uploading, Toast.LENGTH_SHORT).show()
        // Copying needs the share's URI grant, which ends with this
        // activity: copy first (in the background), then finish.
        Thread {
            val msg = runCatching { Uploads.start(this, sources) }.exceptionOrNull()
            runOnUiThread {
                if (msg != null) {
                    Log.w(UploadWorker.TAG, "share failed", msg)
                    Toast.makeText(this, getString(R.string.failed) + ": " + msg.message, Toast.LENGTH_LONG).show()
                }
                finish()
            }
        }.start()
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
