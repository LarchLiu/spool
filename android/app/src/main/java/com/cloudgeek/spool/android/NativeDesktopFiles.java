package com.cloudgeek.spool.android;

import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.graphics.Color;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.concurrent.Executor;
import org.json.JSONArray;
import org.json.JSONObject;

/** Native, view-scoped shared-folder browser. Transfers stream directly through SAF. */
final class NativeDesktopFiles extends FrameLayout implements AutoCloseable {
    interface Transport {
        JSONObject list(String path) throws Exception;
        void upload(String path, String name, InputStream input) throws Exception;
        void download(String path, OutputStream output) throws Exception;
        void cancel();
        void close();
        default boolean closed() { return false; }
    }
    interface Factory { Transport open() throws Exception; }
    private final NativeUi ui;
    private final Context labels;
    private final Executor io;
    private final Factory factory;
    private final NativeDesktopDocuments documents;
    private final Handler main = new Handler(Looper.getMainLooper());
    private final NativeSurface card;
    private final TextView pathLabel, status;
    final NativeUi.IconButton up, refresh, upload, cancel;
    final ListView list;
    private final List<Entry> entries = new ArrayList<>();
    private boolean canUpload, canDownload, busy;
    private volatile boolean closed, cancelled;
    private volatile Transport activeTransport;
    private String path = "";
    private final BaseAdapter adapter = new BaseAdapter() {
        public int getCount() { return entries.size(); }
        public Object getItem(int position) { return entries.get(position); }
        public long getItemId(int position) { return position; }
        public View getView(int position, View recycled, ViewGroup parent) {
            Entry entry = entries.get(position);
            LinearLayout row = ui.row();
            row.setMinimumHeight(ui.dp(64)); row.setPadding(ui.dp(10), ui.dp(5), ui.dp(6), ui.dp(5));
            row.setBackground(ui.states(ui.surface, 12, 0, ui.hover, ui.surface));
            row.setFocusable(true); row.setEnabled(!busy);
            row.setOnClickListener(view -> activate(entry));
            FrameLayout tile = new FrameLayout(ui.context); tile.setBackground(ui.shape(entry.directory ? ui.soft : ui.sidebar, 11, 0));
            ImageView glyph = ui.decorativeIcon(entry.directory ? "folder" : "file", 19);
            glyph.setColorFilter(entry.directory ? ui.primary : ui.muted);
            tile.addView(glyph, new FrameLayout.LayoutParams(ui.dp(22), ui.dp(22), Gravity.CENTER));
            LinearLayout.LayoutParams tileParams = new LinearLayout.LayoutParams(ui.dp(38), ui.dp(38));
            tileParams.setMarginEnd(ui.dp(11)); row.addView(tile, tileParams);
            LinearLayout details = ui.column(); details.setGravity(Gravity.CENTER_VERTICAL);
            TextView name = ui.text(entry.name, 14, ui.text);
            name.setSingleLine(true); name.setEllipsize(android.text.TextUtils.TruncateAt.END);
            details.addView(name, new LinearLayout.LayoutParams(-1, -2));
            if (!entry.directory) {
                TextView size = ui.text(android.text.format.Formatter.formatShortFileSize(ui.context, entry.size), 11, ui.muted);
                size.setPadding(0, ui.dp(3), 0, 0); details.addView(size, new LinearLayout.LayoutParams(-1, -2));
            }
            row.addView(details, new LinearLayout.LayoutParams(0, -2, 1));
            if (canDownload) {
                NativeUi.IconButton save = icon("download", entry.directory ? R.string.desktop_files_download_folder : R.string.desktop_files_download,
                        () -> download(entry));
                save.setContentDescription(text(entry.directory ? R.string.desktop_files_download_folder : R.string.desktop_files_download) + ": " + entry.name);
                save.setLayoutParams(new LinearLayout.LayoutParams(ui.dp(38), ui.dp(38)));
                save.setBackground(ui.states(ui.soft, 12, 0, ui.controlActive, ui.surface));
                save.setEnabled(!busy); row.addView(save);
            }
            return row;
        }
    };

    NativeDesktopFiles(NativeUi ui, Context labels, Executor io, Factory factory, NativeDesktopDocuments documents, Runnable dismiss) {
        super(ui.context);
        this.ui = ui; this.labels = labels; this.io = io; this.factory = factory; this.documents = documents;
        setClipChildren(false); setClipToPadding(false); setBackgroundColor(0x650b0d0a);
        setClickable(true); setContentDescription(text(R.string.desktop_files)); setOnClickListener(view -> dismiss.run());
        card = new NativeSurface(ui, ui.elevated, ui.borderStrong, 18, 0, 10, 32);
        card.setElevation(ui.dp(14)); card.setClickable(true); card.setOnClickListener(view -> {});
        card.setFocusableInTouchMode(true);
        card.setPadding(ui.dp(12), ui.dp(10), ui.dp(12), ui.dp(12));
        addView(card, new FrameLayout.LayoutParams(-1, -1, Gravity.CENTER));

        LinearLayout heading = ui.row(); heading.setPadding(ui.dp(2), ui.dp(2), 0, ui.dp(10));
        FrameLayout badge = new FrameLayout(ui.context); badge.setBackground(ui.shape(ui.soft, 14, 0));
        ImageView folderGlyph = ui.decorativeIcon("folder", 21); folderGlyph.setColorFilter(ui.primary);
        badge.addView(folderGlyph, new FrameLayout.LayoutParams(ui.dp(24), ui.dp(24), Gravity.CENTER));
        LinearLayout.LayoutParams badgeParams = new LinearLayout.LayoutParams(ui.dp(42), ui.dp(42));
        badgeParams.setMarginEnd(ui.dp(12)); heading.addView(badge, badgeParams);
        LinearLayout titleBlock = ui.column(); titleBlock.setGravity(Gravity.CENTER_VERTICAL);
        TextView title = ui.bold(text(R.string.desktop_files), 16); title.setSingleLine(true);
        titleBlock.addView(title, new LinearLayout.LayoutParams(-1, -2));
        TextView subtitle = ui.text(text(R.string.desktop_files_subtitle), 11, ui.muted);
        subtitle.setPadding(0, ui.dp(4), 0, 0); titleBlock.addView(subtitle, new LinearLayout.LayoutParams(-1, -2));
        heading.addView(titleBlock, new LinearLayout.LayoutParams(0, -2, 1));
        NativeUi.IconButton close = icon("close", R.string.desktop_files_close, dismiss);
        close.setLayoutParams(new LinearLayout.LayoutParams(ui.dp(38), ui.dp(38)));
        close.setBackground(ui.states(ui.surface, 12, 0, ui.hover, ui.surface)); heading.addView(close);
        card.addView(heading, new LinearLayout.LayoutParams(-1, -2));

        LinearLayout tools = ui.row(); tools.setPadding(ui.dp(5), ui.dp(5), ui.dp(5), ui.dp(5));
        tools.setBackground(ui.shape(ui.sidebar, 14, ui.line));
        pathLabel = ui.text(text(R.string.desktop_files_root), 12, ui.text);
        pathLabel.setSingleLine(true); pathLabel.setEllipsize(android.text.TextUtils.TruncateAt.MIDDLE);
        pathLabel.setPadding(ui.dp(10), ui.dp(4), ui.dp(6), ui.dp(4));
        tools.addView(pathLabel, new LinearLayout.LayoutParams(0, -2, 1));
        up = icon("arrow_up", R.string.desktop_files_up, () -> load(parent(path)));
        refresh = icon("refresh", R.string.desktop_files_refresh, () -> load(path));
        upload = icon("plus", R.string.desktop_files_upload, this::pickUpload);
        cancel = icon("stop", R.string.desktop_files_cancel, this::cancel);
        for (NativeUi.IconButton action : new NativeUi.IconButton[]{up, refresh, upload, cancel}) {
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(ui.dp(38), ui.dp(38));
            params.setMarginStart(ui.dp(3)); action.setLayoutParams(params); tools.addView(action);
        }
        up.setBackground(ui.states(ui.surface, 11, 0, ui.hover, ui.surface));
        refresh.setBackground(ui.states(ui.surface, 11, 0, ui.hover, ui.surface));
        upload.setImageTintList(android.content.res.ColorStateList.valueOf(Color.WHITE));
        upload.setBackground(ui.states(ui.primary, 11, 0, ui.primaryHover, ui.soft));
        cancel.setImageTintList(android.content.res.ColorStateList.valueOf(ui.error));
        cancel.setBackground(ui.states(ui.errorBg, 11, ui.errorBorder, ui.errorBg, ui.errorBg));
        LinearLayout.LayoutParams toolParams = new LinearLayout.LayoutParams(-1, -2);
        toolParams.bottomMargin = ui.dp(10); card.addView(tools, toolParams);

        status = ui.text("", 12, ui.muted); status.setPadding(ui.dp(12), ui.dp(9), ui.dp(12), ui.dp(9));
        status.setBackground(ui.shape(ui.soft, 11, ui.line));
        status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE); status.setVisibility(GONE);
        LinearLayout.LayoutParams statusParams = new LinearLayout.LayoutParams(-1, -2); statusParams.bottomMargin = ui.dp(8);
        card.addView(status, statusParams);
        list = new ListView(ui.context); list.setDivider(new android.graphics.drawable.ColorDrawable(ui.line)); list.setDividerHeight(ui.dp(1));
        list.setSelector(new android.graphics.drawable.ColorDrawable(Color.TRANSPARENT)); list.setCacheColorHint(Color.TRANSPARENT);
        list.setPadding(ui.dp(2), ui.dp(2), ui.dp(2), ui.dp(2)); list.setClipToPadding(false);
        list.setBackgroundColor(Color.TRANSPARENT); list.setAdapter(adapter);
        list.setContentDescription(text(R.string.desktop_files));
        card.addView(list, new LinearLayout.LayoutParams(-1, 0, 1));
        setVisibility(GONE); controls();
    }

    @Override protected void onMeasure(int widthMeasureSpec, int heightMeasureSpec) {
        int width = resolveSize(MeasureSpec.getSize(widthMeasureSpec), widthMeasureSpec);
        int height = resolveSize(MeasureSpec.getSize(heightMeasureSpec), heightMeasureSpec);
        setMeasuredDimension(width, height);
        int margin = ui.dp(18);
        int cardWidth = Math.min(Math.max(0, width - margin * 2), ui.dp(680));
        int rows = Math.min(entries.size(), 8);
        int desiredHeight = ui.dp(150 + rows * 64 + (status != null && status.getVisibility() == VISIBLE ? 42 : 0));
        int cardHeight = Math.min(Math.max(0, height - margin * 2), Math.min(ui.dp(700), Math.max(ui.dp(260), desiredHeight)));
        card.measure(MeasureSpec.makeMeasureSpec(cardWidth, MeasureSpec.EXACTLY),
                MeasureSpec.makeMeasureSpec(cardHeight, MeasureSpec.EXACTLY));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int x = (getWidth() - card.getMeasuredWidth()) / 2;
        int y = (getHeight() - card.getMeasuredHeight()) / 2;
        card.layout(x, y, x + card.getMeasuredWidth(), y + card.getMeasuredHeight());
    }

    private String text(int resource) { return labels.getString(resource); }
    private void activate(Entry entry) {
        if (busy || closed) return;
        if (entry.directory) load(join(path, entry.name));
        else if (canDownload) download(entry);
    }
    private NativeUi.IconButton icon(String kind, int label, Runnable action) {
        NativeUi.IconButton button = ui.icon(kind, 20, text(label), action);
        button.setTooltipText(text(label)); button.setBackground(ui.states(ui.surface, 11, 0, ui.hover, ui.surface)); return button;
    }
    boolean available() { return !closed && (canUpload || canDownload); }
    String directory() { return path; }
    void configure(JSONObject config, boolean readOnly) {
        boolean nextUpload = config != null && config.optBoolean("upload") && !readOnly;
        boolean nextDownload = config != null && config.optBoolean("download");
        if (busy && (canUpload && !nextUpload || canDownload && !nextDownload)) cancel();
        canUpload = nextUpload; canDownload = nextDownload;
        if (config != null && entries.isEmpty() && path.isEmpty()) {
            String initial = config.optString("dir");
            if (validPath(initial)) path = initial;
        }
        if (!available()) setVisibility(GONE);
        controls();
    }
    void open() { if (available()) { setVisibility(VISIBLE); card.requestFocus(); if (!busy) load(path); } }
    void hide() { setVisibility(GONE); }
    private void controls() {
        up.setEnabled(!busy && !path.isEmpty()); refresh.setEnabled(!busy && available());
        upload.setVisibility(canUpload ? VISIBLE : GONE); upload.setEnabled(!busy && available());
        cancel.setVisibility(busy ? VISIBLE : GONE); cancel.setEnabled(busy && !cancelled);
        list.setEnabled(!busy); adapter.notifyDataSetChanged();
    }
    private boolean begin(int message) {
        if (busy || !available()) return false;
        busy = true; cancelled = false; status.setVisibility(VISIBLE); status.setTextColor(ui.muted);
        status.setBackground(ui.shape(ui.soft, 11, ui.line)); status.setText(text(message)); controls(); return true;
    }
    private void finish(boolean success) {
        if (closed) return;
        activeTransport = null; busy = false;
        status.setTextColor(success || cancelled ? ui.muted : ui.error);
        status.setBackground(ui.shape(success || cancelled ? ui.soft : ui.errorBg, 11, success || cancelled ? ui.line : ui.errorBorder));
        status.setText(text(cancelled ? R.string.desktop_files_cancelled : success ? R.string.desktop_files_done : R.string.desktop_files_error));
        controls();
    }
    private Transport transport() throws Exception {
        if (closed || cancelled) throw new java.io.IOException("Desktop files closed");
        Transport transport = factory.open(); activeTransport = transport;
        if (closed || cancelled) { transport.cancel(); throw new java.io.IOException("Desktop files closed"); }
        return transport;
    }
    void load(String requested) {
        if (!validPath(requested) || !begin(R.string.desktop_files_loading)) return;
        io.execute(() -> {
            try {
                JSONObject response = transport().list(requested);
                String actual = response.getString("path");
                if (!validPath(actual)) throw new IllegalArgumentException("Invalid shared-folder path");
                JSONArray values = response.getJSONArray("entries");
                if (values.length() > 5000) throw new IllegalArgumentException("Too many files");
                List<Entry> loaded = new ArrayList<>();
                for (int i = 0; i < values.length(); i++) {
                    JSONObject value = values.getJSONObject(i); String name = value.getString("name");
                    if (validName(name)) loaded.add(new Entry(name, value.optBoolean("dir"), Math.max(0, value.optLong("size"))));
                }
                loaded.sort(Comparator.comparing((Entry entry) -> !entry.directory).thenComparing(entry -> entry.name, String.CASE_INSENSITIVE_ORDER));
                main.post(() -> {
                    if (closed) return;
                    if (!cancelled) {
                        path = actual; pathLabel.setText(path.isEmpty() ? text(R.string.desktop_files_root) : path);
                        entries.clear(); entries.addAll(loaded);
                    }
                    finish(true);
                    if (!cancelled) {
                        if (entries.isEmpty()) { status.setVisibility(VISIBLE); status.setText(text(R.string.desktop_files_empty)); }
                        else status.setVisibility(GONE);
                    }
                    requestLayout();
                });
            } catch (Exception error) { main.post(() -> finish(false)); }
        });
    }
    private void pickUpload() {
        if (!canUpload || !begin(R.string.desktop_files_uploading)) return;
        String directory = path;
        try {
            documents.pick(new Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), uri -> {
                if (closed) return;
                if (uri == null) { cancelled = true; finish(false); return; }
                io.execute(() -> {
                    try {
                        requireDocument(uri);
                        String name = "upload";
                        try (Cursor cursor = ui.context.getContentResolver().query(uri, new String[]{OpenableColumns.DISPLAY_NAME}, null, null, null)) {
                            if (cursor != null && cursor.moveToFirst()) name = cursor.getString(0);
                        }
                        if (!validName(name)) throw new IllegalArgumentException("Invalid document name");
                        Transport transport = transport();
                        try (InputStream input = ui.context.getContentResolver().openInputStream(uri)) {
                            if (input == null) throw new java.io.IOException("Cannot open document");
                            transport.upload(directory, name, input);
                        }
                        main.post(() -> { if (!closed) { boolean reload = !cancelled; finish(true); if (reload) load(directory); } });
                    } catch (Exception error) { main.post(() -> finish(false)); }
                });
            });
        } catch (RuntimeException error) { finish(false); }
    }
    private void download(Entry entry) {
        if (!canDownload || !begin(R.string.desktop_files_downloading)) return;
        String source = join(path, entry.name);
        try {
            documents.pick(new Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
                    .setType(entry.directory ? "application/zip" : "application/octet-stream")
                    .putExtra(Intent.EXTRA_TITLE, entry.name + (entry.directory ? ".zip" : "")), uri -> {
                if (closed) return;
                if (uri == null) { cancelled = true; finish(false); return; }
                io.execute(() -> {
                    boolean complete = false;
                    try {
                        requireDocument(uri);
                        Transport transport = transport();
                        try (OutputStream output = ui.context.getContentResolver().openOutputStream(uri, "wt")) {
                            if (output == null) throw new java.io.IOException("Cannot open document");
                            transport.download(source, output);
                        }
                        complete = !cancelled && !closed;
                        main.post(() -> finish(true));
                    } catch (Exception error) { main.post(() -> finish(false)); }
                    finally {
                        // ACTION_CREATE_DOCUMENT created this destination for this operation.
                        // Remove partial downloads instead of leaving a successful-looking file.
                        if (!complete && "content".equals(uri.getScheme())) try {
                            android.provider.DocumentsContract.deleteDocument(ui.context.getContentResolver(), uri);
                        } catch (Exception ignored) {}
                    }
                });
            });
        } catch (RuntimeException error) { finish(false); }
    }
    private static void requireDocument(Uri uri) throws java.io.IOException {
        if (!"content".equals(uri.getScheme())) throw new java.io.IOException("Only document-provider files are supported");
    }
    void cancel() {
        cancelled = true;
        Transport transport = activeTransport; if (transport != null) transport.cancel();
        controls();
    }
    @Override public void close() { closed = true; cancel(); main.removeCallbacksAndMessages(null); setVisibility(GONE); }
    private record Entry(String name, boolean directory, long size) {}
    static String join(String path, String name) { return path.isEmpty() ? name : path + "/" + name; }
    static String parent(String path) { int separator = path.lastIndexOf('/'); return separator < 0 ? "" : path.substring(0, separator); }
    static boolean validPath(String path) {
        if (path == null || path.getBytes(StandardCharsets.UTF_8).length > 16384) return false;
        if (path.isEmpty()) return true;
        for (String part : path.split("/", -1)) if (!validName(part)) return false;
        return true;
    }
    static boolean validName(String name) {
        return name != null && !name.isEmpty() && !name.equals(".") && !name.equals("..")
                && name.getBytes(StandardCharsets.UTF_8).length <= 255 && name.indexOf('/') < 0 && name.indexOf('\\') < 0
                && name.codePoints().noneMatch(Character::isISOControl);
    }
}
