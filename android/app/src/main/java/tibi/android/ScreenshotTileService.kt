package tibi.android

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.service.quicksettings.TileService
import android.widget.Toast

/** Quick Settings tile: upload the most recent screenshot. */
class ScreenshotTileService : TileService() {
    override fun onClick() {
        if (!Settings(this).configured || checkSelfPermission(SettingsActivity.mediaPermission()) != PackageManager.PERMISSION_GRANTED) {
            openSettings()
            return
        }
        val uri = latestScreenshot(this)
        if (uri == null) {
            Toast.makeText(this, R.string.no_screenshot, Toast.LENGTH_LONG).show()
            return
        }
        Toast.makeText(this, R.string.uploading, Toast.LENGTH_SHORT).show()
        Thread { runCatching { Uploads.start(this, listOf(Source.Content(uri, null))) } }.start()
    }

    @SuppressLint("StartActivityAndCollapseDeprecated")
    private fun openSettings() {
        val i = Intent(this, SettingsActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(PendingIntent.getActivity(this, 0, i, PendingIntent.FLAG_IMMUTABLE))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(i)
        }
    }

    companion object {
        fun latestScreenshot(ctx: Context): Uri? {
            val col = MediaStore.Images.Media.EXTERNAL_CONTENT_URI
            val (where, args) = if (Build.VERSION.SDK_INT >= 29) {
                "${MediaStore.Images.Media.RELATIVE_PATH} LIKE ?" to arrayOf("%Screenshots%")
            } else {
                @Suppress("DEPRECATION")
                "${MediaStore.Images.Media.DATA} LIKE ?" to arrayOf("%/Screenshots/%")
            }
            ctx.contentResolver.query(col, arrayOf(MediaStore.Images.Media._ID), where, args,
                "${MediaStore.Images.Media.DATE_ADDED} DESC")?.use { c ->
                if (c.moveToFirst()) return ContentUris.withAppendedId(col, c.getLong(0))
            }
            return null
        }
    }
}
