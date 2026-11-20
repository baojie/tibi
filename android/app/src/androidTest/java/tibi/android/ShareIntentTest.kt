package tibi.android

import android.content.ClipData
import android.content.Intent
import android.net.Uri
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Sends ShareActivity what a gallery's share sheet sends for several
 * photos (SEND_MULTIPLE, EXTRA_STREAM list, ClipData, read grant). `adb
 * shell am start` cannot build such an intent; scripts/emulator-test.sh
 * runs this with `-e uris content://…,content://…`.
 *
 * Instrumentation kills the app process when it ends, so this waits until
 * the share was copied into the outbox and uploaded (the job removed).
 */
@RunWith(AndroidJUnit4::class)
class ShareIntentTest {
    @Test
    fun shareMultiple() {
        val arg = InstrumentationRegistry.getArguments().getString("uris")
        assumeTrue("needs -e uris", !arg.isNullOrEmpty())
        val uris = ArrayList(arg!!.split(",").map(Uri::parse))
        val ctx = InstrumentationRegistry.getInstrumentation().targetContext
        val clip = ClipData.newRawUri(null, uris[0]).apply { uris.drop(1).forEach { addItem(ClipData.Item(it)) } }
        val i = Intent(Intent.ACTION_SEND_MULTIPLE)
            .setClass(ctx, ShareActivity::class.java)
            .setType("image/*")
            .putParcelableArrayListExtra(Intent.EXTRA_STREAM, uris)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_GRANT_READ_URI_PERMISSION)
        i.clipData = clip
        val before = Job.root(ctx).list()!!.toSet()
        ctx.startActivity(i)
        val deadline = System.currentTimeMillis() + 180_000
        var seen: String? = null
        while (System.currentTimeMillis() < deadline) {
            val now = Job.root(ctx).list()!!.toSet()
            if (seen == null) seen = (now - before).firstOrNull()
            else if (seen !in now) return
            Thread.sleep(200)
        }
        throw AssertionError("share not uploaded in time (job ${seen ?: "never created"})")
    }
}
