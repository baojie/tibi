package tibi.android

import android.Manifest
import android.app.Activity
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.TextView

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
