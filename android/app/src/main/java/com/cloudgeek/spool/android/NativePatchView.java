package com.cloudgeek.spool.android;

import android.content.res.Configuration;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.Typeface;
import android.text.TextUtils;
import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.lang.ref.WeakReference;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.FutureTask;
import com.cloudgeek.spool.android.NativeDiffLayout.Cell;
import com.cloudgeek.spool.android.NativeDiffLayout.Row;

/** Native file diffs with paired, horizontally scrollable columns and complete selectable raw source. */
final class NativePatchView extends LinearLayout {
    static final class State {
        byte[] sourceHash;
        boolean rawOpen;
        void bind(String value) {
            try {
                byte[] hash = java.security.MessageDigest.getInstance("SHA-256").digest(value.getBytes(java.nio.charset.StandardCharsets.UTF_8));
                if (!java.util.Arrays.equals(sourceHash, hash)) rawOpen = false;
                // Disclosure state must not retain full tool outputs after navigating away.
                sourceHash = hash;
            } catch (java.security.NoSuchAlgorithmException impossible) { throw new IllegalStateException(impossible); }
        }
    }
    private final NativeUi ui;
    private final NativeDiffPalette palette;
    private final boolean diffDark;
    private final List<FileRender> renders = new ArrayList<>();
    private LayoutTask layoutTask;
    private int layoutEpoch;
    private boolean precise;

    private final class FileRender {
        final NativePatch.File file;
        List<Row> rows;
        final List<Code> views = new ArrayList<>();
        FileRender(NativePatch.File file) { this.file = file; rows = NativeDiffLayout.rows(file, ui.codeWorker == null); }
    }

    private static final class LayoutTask extends FutureTask<List<List<Row>>> {
        private final WeakReference<NativePatchView> target;
        private final int epoch;
        private final NativeSyntax syntax;
        private final int syntaxEpoch;
        LayoutTask(NativePatchView view, List<NativePatch.File> files, int epoch, NativeSyntax syntax, int syntaxEpoch, boolean dark) {
            super(() -> {
                List<List<Row>> result = new ArrayList<>();
                for (NativePatch.File file : files) {
                    List<Row> rows = NativeDiffLayout.rows(file);
                    if (syntax != null) try { syntax.decorateDiff(file, rows, dark, syntaxEpoch); }
                    catch (java.util.concurrent.CancellationException canceled) { throw canceled; }
                    catch (Exception unavailable) {
                        for (Row row : rows) { if (row.old != null) row.old.syntax = java.util.Collections.emptyList(); if (row.next != null) row.next.syntax = java.util.Collections.emptyList(); }
                    }
                    result.add(rows);
                }
                return result;
            });
            target = new WeakReference<>(view); this.epoch = epoch;
            this.syntax = syntax; this.syntaxEpoch = syntaxEpoch;
        }
        @Override protected void done() {
            if (isCancelled()) return;
            final List<List<Row>> result;
            try { result = get(); } catch (Exception ignored) { return; }
            new android.os.Handler(android.os.Looper.getMainLooper()).post(() -> {
                NativePatchView view = target.get();
                if (view == null || view.layoutEpoch != epoch || !view.isAttachedToWindow()) return;
                if (syntax != null && !syntax.current(syntaxEpoch)) return;
                view.precise = true; view.layoutTask = null;
                for (int file = 0; file < result.size(); file++) {
                    FileRender render = view.renders.get(file);
                    render.rows = result.get(file);
                    for (Code code : render.views) {
                        // Similarity realignment preserves row counts, heights and each pane's maximum width.
                        code.rows = render.rows; code.invalidate();
                    }
                }
            });
        }
    }

    @Override protected void onAttachedToWindow() {
        super.onAttachedToWindow();
        if (precise || ui.codeWorker == null || renders.isEmpty()) return;
        List<NativePatch.File> files = new ArrayList<>();
        for (FileRender render : renders) files.add(render.file);
        layoutTask = new LayoutTask(this, files, ++layoutEpoch, ui.syntax, ui.syntax == null ? 0 : ui.syntax.epoch(), diffDark);
        try { ui.codeWorker.execute(layoutTask); }
        catch (java.util.concurrent.RejectedExecutionException ignored) { layoutTask.cancel(false); layoutTask = null; }
    }

    @Override protected void onDetachedFromWindow() {
        ++layoutEpoch;
        if (layoutTask != null) { layoutTask.cancel(true); layoutTask = null; }
        super.onDetachedFromWindow();
    }

    static View render(NativeUi ui, String source, boolean input) {
        return render(ui, source, input, new State());
    }

    static View render(NativeUi ui, String source, boolean input, State state) {
        state.bind(source);
        List<NativePatch.File> files = NativePatch.parse(source);
        return files.isEmpty() ? NativeActivityList.raw(ui, source) : new NativePatchView(ui, source, input, files, state);
    }

    static View bind(NativeUi ui, View current, String source, boolean input, State state) {
        if (current instanceof android.widget.ScrollView && NativePatch.parse(source).isEmpty()) {
            state.bind(source);
            ((NativeTextView) ((android.widget.ScrollView) current).getChildAt(0)).bindMarkdownText(source);
            return current;
        }
        // Web's ToolDiffBoundary is keyed by the complete source; changed diffs remount.
        return render(ui, source, input, state);
    }

    private NativePatchView(NativeUi ui, String source, boolean input, List<NativePatch.File> files, State state) {
        super(ui.context);
        this.ui = ui;
        // FileDiff's default follows prefers-color-scheme, independently of the workspace preference.
        diffDark = (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
        palette = new NativeDiffPalette(diffDark);
        setOrientation(VERTICAL);
        for (int i = 0; i < files.size(); i++) {
            if (i > 0) addView(ui.space(8));
            addView(file(files.get(i)), new LayoutParams(-1, -2));
        }
        LinearLayout raw = ui.column();
        TextView toggle = ui.text("", 11, ui.subtle);
        String title = input ? ui.t("Raw input") : ui.t("Raw output");
        toggle.setContentDescription(title);
        toggle.setFocusable(true);
        raw.addView(ui.space(6)); raw.addView(NativeActivityList.raw(ui, source));
        Runnable update = () -> {
            // Native <details>: hiding its body does not unmount the selectable source.
            raw.setVisibility(state.rawOpen ? VISIBLE : GONE);
            toggle.setText((state.rawOpen ? "\u25bc " : "\u25b6 ") + title);
            if (android.os.Build.VERSION.SDK_INT >= 30) toggle.setStateDescription(ui.label(state.rawOpen ? "Expanded" : "Collapsed", state.rawOpen ? "已展开" : "已折叠"));
        };
        toggle.setOnClickListener(view -> { state.rawOpen = !state.rawOpen; update.run(); });
        update.run();
        LayoutParams rawParams = new LayoutParams(-1, -2);
        rawParams.topMargin = ui.dp(8);
        addView(toggle, rawParams);
        addView(raw);
    }

    private View file(NativePatch.File file) {
        LinearLayout card = ui.column();
        card.setBackground(ui.shape(palette.bg, 7, ui.line));
        card.setClipToOutline(true);
        card.setPadding(ui.dp(1), ui.dp(1), ui.dp(1), ui.dp(1));
        LinearLayout header = ui.row();
        header.setPadding(ui.dp(16), ui.dp(12), ui.dp(16), ui.dp(12));
        String icon = file.type.equals("new") ? "diff-symbol-added" : file.type.equals("deleted") ? "diff-symbol-deleted"
                : file.type.startsWith("rename-") ? "diff-symbol-moved" : "diff-symbol-modified";
        int color = file.type.equals("new") ? palette.added : file.type.equals("deleted") ? palette.deleted : palette.modified;
        android.widget.ImageView image = ui.decorativeIcon(icon, 16);
        image.setImageDrawable(NativeIcons.drawable(ui.context, icon, color, ui.dp(16)));
        header.addView(image, new LayoutParams(ui.dp(16), ui.dp(16)));
        LinearLayout titles = ui.row();
        if (!file.prevName.isEmpty()) {
            TextView previous = filename(file.prevName);
            previous.setAlpha(.7f);
            titles.addView(previous, new LayoutParams(0, -2, 1));
            android.widget.ImageView arrow = ui.decorativeIcon("diff-arrow-right-short", 16);
            arrow.setImageDrawable(NativeIcons.drawable(ui.context, "diff-arrow-right-short", palette.fg, ui.dp(16)));
            LayoutParams arrowParams = new LayoutParams(ui.dp(16), ui.dp(16));
            arrowParams.setMarginStart(ui.dp(8));
            arrowParams.setMarginEnd(ui.dp(8));
            titles.addView(arrow, arrowParams);
        }
        TextView name = filename(file.name);
        titles.addView(name, new LayoutParams(0, -2, 1));
        LayoutParams titleParams = new LayoutParams(0, -2, 1);
        titleParams.setMarginStart(ui.dp(8));
        header.addView(titles, titleParams);
        int additions = file.additions(), deletions = file.deletions();
        if (deletions > 0 || additions == 0) header.addView(stat("-" + deletions, palette.deleted));
        if (additions > 0 || deletions == 0) header.addView(stat("+" + additions, palette.added));
        card.addView(header, new LayoutParams(-1, -2));
        if (!file.hunks.isEmpty()) {
            FileRender render = new FileRender(file);
            renders.add(render);
            LinearLayout body = ui.row();
            body.setGravity(Gravity.TOP);
            if (file.type.equals("new") || file.type.equals("deleted")) {
                body.addView(new Pane(render, file.type.equals("new")), new LayoutParams(-1, -2));
            } else {
                Pane old = new Pane(render, false), next = new Pane(render, true);
                old.peer = next;
                next.peer = old;
                body.addView(old, new LayoutParams(0, -2, 1));
                body.addView(next, new LayoutParams(0, -2, 1));
            }
            card.addView(body, new LayoutParams(-1, -2));
        }
        if (file.contentsUnavailable) {
            TextView note = ui.text(ui.t("File contents are not included in this patch."), 12, ui.subtle);
            LayoutParams params = new LayoutParams(-1, -2);
            params.setMargins(ui.dp(12), ui.dp(10), ui.dp(12), ui.dp(10));
            card.addView(note, params);
        }
        return card;
    }

    private TextView filename(String title) {
        TextView name = ui.text(title, 13, palette.fg);
        ui.fixedLineHeight(name, 20f / 13);
        name.setSingleLine(true);
        name.setEllipsize(TextUtils.TruncateAt.START);
        name.setContentDescription(title);
        return name;
    }

    private TextView stat(String text, int color) {
        TextView view = ui.text(text, 13, color);
        view.setTypeface(Typeface.MONOSPACE);
        ui.fixedLineHeight(view, 20f / 13);
        LayoutParams params = new LayoutParams(-2, -2);
        params.setMarginStart(ui.dp(8));
        view.setLayoutParams(params);
        return view;
    }

    private final class Pane extends FrameLayout {
        final Code code;
        final HorizontalScrollView scroll;
        Pane peer;
        boolean syncing;
        Pane(FileRender render, boolean additions) {
            super(ui.context);
            setBackgroundColor(palette.bg);
            code = new Code(render.file, render.rows, additions, false);
            render.views.add(code);
            scroll = new HorizontalScrollView(ui.context) {
                @Override protected void onScrollChanged(int x, int y, int oldX, int oldY) {
                    super.onScrollChanged(x, y, oldX, oldY);
                    if (peer != null && !syncing && peer.scroll.getScrollX() != x) {
                        peer.syncing = true;
                        peer.scroll.scrollTo(x, 0);
                        peer.syncing = false;
                    }
                }
            };
            scroll.setFillViewport(true);
            scroll.setHorizontalScrollBarEnabled(false);
            scroll.addView(code, new FrameLayout.LayoutParams(-2, -2));
            FrameLayout.LayoutParams viewport = new FrameLayout.LayoutParams(-1, -2);
            viewport.leftMargin = ui.dp(additions ? 1 : 0);
            viewport.rightMargin = ui.dp(additions ? 0 : 1);
            addView(scroll, viewport);
            Code gutter = new Code(render.file, render.rows, additions, true);
            render.views.add(gutter);
            FrameLayout.LayoutParams numbers = new FrameLayout.LayoutParams(gutter.gutterWidth, -2);
            numbers.leftMargin = ui.dp(additions ? 1 : 0);
            addView(gutter, numbers);
            setContentDescription((additions ? ui.label("Additions", "新增内容") : ui.label("Deletions", "删除内容")) + ": " + render.file.name);
        }
    }

    /** Draw only visible lines; a 25,000-line tool patch must not allocate 25,000 Android views. */
    private final class Code extends View {
        List<Row> rows;
        final boolean additions, gutter;
        final Paint text = new Paint(Paint.ANTI_ALIAS_FLAG), fill = new Paint();
        final int lineHeight, gutterWidth, contentWidth;
        final int[] tops;
        final Rect clip = new Rect();
        Code(NativePatch.File file, List<Row> rows, boolean additions, boolean gutter) {
            super(ui.context);
            this.rows = rows;
            this.additions = additions;
            this.gutter = gutter;
            text.setTypeface(Typeface.MONOSPACE);
            text.setLinearText(true); text.setSubpixelText(true);
            text.setTextSize(13 * getResources().getDisplayMetrics().scaledDensity);
            lineHeight = Math.round(text.getTextSize() * 20f / 13);
            int digits = 1;
            for (Row row : rows) {
                Cell cell = additions ? row.next : row.old;
                if (cell != null) digits = Math.max(digits, String.valueOf(cell.number).length());
            }
            gutterWidth = file.lineNumbersUnavailable ? ui.dp(6) : Math.round(text.measureText("0") * (digits + 3)) + ui.dp(2);
            tops = new int[rows.size() + 1];
            float width = 0;
            for (int i = 0; i < rows.size(); i++) {
                Row row = rows.get(i);
                tops[i + 1] = tops[i] + (row.separator != null ? ui.dp(row.simple ? 4 : 32) : lineHeight);
                Cell cell = additions ? row.next : row.old;
                if (cell != null) { String value = plain(cell.text); width = Math.max(width, NativeCodeTabs.width(text, value, value.length())); }
            }
            contentWidth = Math.round(width + text.measureText("0") * 2) + gutterWidth;
            setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_NO);
        }
        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            setMeasuredDimension(resolveSize(gutter ? gutterWidth : contentWidth, widthSpec), resolveSize(tops[rows.size()] + ui.dp(8), heightSpec));
        }
        @Override protected void onDraw(Canvas canvas) {
            canvas.getClipBounds(clip);
            canvas.drawColor(palette.bg);
            int first = java.util.Arrays.binarySearch(tops, clip.top);
            if (first < 0) first = Math.max(0, -first - 2);
            for (int i = first; i < rows.size() && tops[i] <= clip.bottom; i++) {
                Row row = rows.get(i);
                int y = tops[i], bottom = tops[i + 1];
                if (row.separator != null) {
                    fill.setColor(palette.separator);
                    canvas.drawRect(0, y, getWidth(), bottom, fill);
                    if (!row.simple && !gutter) {
                        text.setColor(palette.number);
                        canvas.drawText(row.separator, gutterWidth + ui.dp(8), y + (bottom - y - text.ascent() - text.descent()) / 2, text);
                    }
                    continue;
                }
                Cell cell = additions ? row.next : row.old;
                int background = cell == null ? palette.bg : cell.kind == '+' ? gutter ? palette.additionGutter : palette.additionBg
                        : cell.kind == '-' ? gutter ? palette.deletionGutter : palette.deletionBg : palette.bg;
                fill.setColor(background);
                canvas.drawRect(0, y, getWidth(), bottom, fill);
                if (cell == null) {
                    if (!gutter) {
                        canvas.save();
                        canvas.clipRect(gutterWidth, y, getWidth(), bottom);
                        fill.setColor(palette.buffer);
                        fill.setStrokeWidth(ui.dp(1));
                        for (int x = gutterWidth - lineHeight; x < getWidth(); x += ui.dp(8)) canvas.drawLine(x, bottom, x + lineHeight, y, fill);
                        canvas.restore();
                    }
                    continue;
                }
                float baseline = y + (lineHeight - text.ascent() - text.descent()) / 2;
                if (gutter) {
                    if (cell.kind == '+') {
                        fill.setColor(palette.added);
                        canvas.drawRect(0, y, ui.dp(4), bottom, fill);
                    } else if (cell.kind == '-') {
                        fill.setColor(palette.deleted);
                        int stripe = Math.max(1, ui.dp(1));
                        for (int at = y; at < bottom; at += stripe * 2) canvas.drawRect(0, at, ui.dp(4), Math.min(at + stripe, bottom), fill);
                    }
                    if (gutterWidth > ui.dp(6) && cell.number > 0) {
                        String number = String.valueOf(cell.number);
                        text.setColor(cell.kind == '+' ? palette.added : cell.kind == '-' ? palette.deleted : palette.number);
                        canvas.drawText(number, gutterWidth - text.measureText("0") - ui.dp(2) - text.measureText(number), baseline, text);
                    }
                    fill.setColor(palette.bg);
                    canvas.drawRect(getWidth() - ui.dp(2), y, getWidth(), bottom, fill);
                } else {
                    for (NativeWordDiff.Range range : cell.emphasis) {
                        String value = plain(cell.text);
                        float start = gutterWidth + text.measureText("0") + NativeCodeTabs.width(text, value, Math.min(value.length(), range.start));
                        float end = gutterWidth + text.measureText("0") + NativeCodeTabs.width(text, value, Math.min(value.length(), range.end));
                        fill.setColor(cell.kind == '+' ? palette.additionEmphasis : palette.deletionEmphasis);
                        canvas.drawRoundRect(start, baseline + text.ascent(), end, baseline + text.descent(), ui.dp(3), ui.dp(3), fill);
                    }
                    text.setColor(cell.kind == 'n' ? palette.number : palette.fg);
                    NativeSyntax.draw(canvas, text, plain(cell.text), cell.syntax, gutterWidth + text.measureText("0"), baseline);
                }
            }
        }
        private String plain(String value) { return NativeWordDiff.cleanLastNewline(value); }
    }
}
