package tibi.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.widget.Toast

/** Notification actions. */
class ActionReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action == COPY) {
            Uploads.copy(ctx, intent.getStringExtra("url") ?: return)
            Toast.makeText(ctx, R.string.copied, Toast.LENGTH_SHORT).show()
        }
    }

    companion object {
        const val COPY = "tibi.android.COPY"
    }
}
