package tibi.android

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.widget.Button
import android.view.Gravity
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast

/** Launcher screen: server URL, token, connection test, permissions. */
class SettingsActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.settings)
        val s = Settings(this)
        val server = findViewById<EditText>(R.id.server)
        val token = findViewById<EditText>(R.id.token)
        val status = findViewById<TextView>(R.id.status)
        server.setText(s.server)
        token.setText(s.token)
        findViewById<Button>(R.id.save).setOnClickListener {
            s.server = server.text.toString()
            s.token = token.text.toString()
            status.setText(R.string.testing)
            Thread {
                val r = runCatching { s.api().whoami() }
                runOnUiThread {
                    status.text = r.fold(
                        { getString(R.string.connected_as, it) },
                        { getString(R.string.connect_failed, it.message ?: it.javaClass.simpleName) })
                }
            }.start()
        }
        findViewById<Button>(R.id.media).setOnClickListener {
            requestPermissions(arrayOf(mediaPermission()), 2)
        }
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
    }

    override fun onResume() {
        super.onResume()
        findViewById<Button>(R.id.media).isEnabled = checkSelfPermission(mediaPermission()) != PackageManager.PERMISSION_GRANTED
        showRecent()
    }

    /** The last links, so one missed in the notification can still be shared: tap copies. */
    private fun showRecent() {
        val box = findViewById<LinearLayout>(R.id.recent)
        box.removeAllViews()
        val list = Settings(this).recent()
        if (list.isEmpty()) {
            box.addView(TextView(this).apply { setText(R.string.no_recent); alpha = 0.6f })
            return
        }
        for ((url, label) in list) {
            val row = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.CENTER_VERTICAL }
            val text = TextView(this).apply {
                text = "$label\n$url"
                setPadding(0, 12, 0, 12)
                setOnClickListener {
                    Uploads.copy(this@SettingsActivity, url)
                    Toast.makeText(this@SettingsActivity, R.string.copied, Toast.LENGTH_SHORT).show()
                }
            }
            row.addView(text, LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
            row.addView(Button(this, null, android.R.attr.borderlessButtonStyle).apply {
                setText(R.string.share)
                setOnClickListener {
                    startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, url), null))
                }
            })
            box.addView(row, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        onResume()
    }

    companion object {
        fun mediaPermission() =
            if (Build.VERSION.SDK_INT >= 33) Manifest.permission.READ_MEDIA_IMAGES else Manifest.permission.READ_EXTERNAL_STORAGE
    }
}
