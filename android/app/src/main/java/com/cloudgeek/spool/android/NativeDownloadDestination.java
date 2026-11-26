package com.cloudgeek.spool.android;

import android.content.ContentResolver;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.os.Environment;
import android.provider.DocumentsContract;
import android.provider.OpenableColumns;
import java.io.File;

/** Describe the actual SAF result; cloud providers need not expose a local path. */
final class NativeDownloadDestination {
    static String describe(Context context, Uri destination, String fallback) {
        ContentResolver resolver = context.getContentResolver();
        String name = fallback;
        try (Cursor cursor = resolver.query(destination, new String[]{OpenableColumns.DISPLAY_NAME}, null, null, null)) {
            if (cursor != null && cursor.moveToFirst() && !cursor.isNull(0) && !cursor.getString(0).isEmpty()) name = cursor.getString(0);
        } catch (RuntimeException ignored) { /* A completed write does not depend on metadata support. */ }
        try {
            String id = DocumentsContract.getDocumentId(destination);
            if ("com.android.externalstorage.documents".equals(destination.getAuthority())) {
                int colon = id.indexOf(':');
                if (colon > 0) {
                    String volume = id.substring(0, colon), relative = id.substring(colon + 1);
                    File root = volume.equals("primary") ? Environment.getExternalStorageDirectory() : new File("/storage", volume);
                    return new File(root, relative).getPath();
                }
            }
            if ("com.android.providers.downloads.documents".equals(destination.getAuthority()) && id.startsWith("raw:")) return id.substring(4);
        } catch (RuntimeException ignored) { /* Providers can return opaque identifiers. */ }
        try {
            android.content.pm.ProviderInfo provider = context.getPackageManager().resolveContentProvider(destination.getAuthority(), 0);
            if (provider != null) {
                CharSequence label = provider.loadLabel(context.getPackageManager());
                if (label != null && label.length() > 0) return label + " / " + name;
            }
        } catch (RuntimeException ignored) { /* The actual filename is still useful. */ }
        return name;
    }
}
