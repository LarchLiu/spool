package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.Typeface;
import android.os.Handler;
import android.os.Looper;
import android.text.Spannable;
import android.text.SpannableString;
import android.text.Spanned;
import android.text.style.ForegroundColorSpan;
import android.text.style.StrikethroughSpan;
import android.text.style.StyleSpan;
import android.text.style.UnderlineSpan;
import android.view.View;
import android.widget.TextView;
import java.lang.ref.WeakReference;
import java.util.List;
import java.util.concurrent.Executor;
import java.util.concurrent.FutureTask;
import java.util.function.Consumer;

/** Shared async/native syntax service and render adapters; selection/tab bytes never change. */
final class NativeSyntax implements AutoCloseable {
    interface Highlighter { List<NativeSyntaxEngine.Line> compute(String path, String source, boolean dark) throws Exception; }
    private final Executor worker;
    private final Highlighter highlighter, codeHighlighter;
    private final Runnable cleanup;
    private volatile int epoch;
    private volatile boolean closed;

    NativeSyntax(Executor worker, NativeSyntaxEngine engine) { this(worker, engine::highlight, engine::highlightCode, engine::close); }
    NativeSyntax(Executor worker, Highlighter highlighter, Runnable cleanup) { this(worker, highlighter, highlighter, cleanup); }
    private NativeSyntax(Executor worker, Highlighter highlighter, Highlighter codeHighlighter, Runnable cleanup) {
        this.worker = worker; this.highlighter = highlighter; this.codeHighlighter = codeHighlighter; this.cleanup = cleanup;
    }
    int epoch() { return epoch; }
    boolean current(int token) { return !closed && epoch == token; }
    List<NativeSyntaxEngine.Line> compute(String path, String source, boolean dark, int token) throws Exception {
        return compute(highlighter, path, source, dark, token);
    }
    List<NativeSyntaxEngine.Line> computeCode(String language, String source, boolean dark, int token) throws Exception {
        return compute(codeHighlighter, language, source, dark, token);
    }
    private List<NativeSyntaxEngine.Line> compute(Highlighter renderer, String path, String source, boolean dark, int token) throws Exception {
        if (!current(token) || Thread.currentThread().isInterrupted()) throw new java.util.concurrent.CancellationException();
        List<NativeSyntaxEngine.Line> result = renderer.compute(path, source, dark);
        if (!current(token) || Thread.currentThread().isInterrupted()) throw new java.util.concurrent.CancellationException();
        return result;
    }
    Binding bind(View view, String path, String contents, boolean dark, Consumer<List<NativeSyntaxEngine.Line>> apply) {
        return bind(view, highlighter, path, contents, dark, apply);
    }
    Binding bindCode(TextView view, String language, String contents, boolean dark) {
        return bind(view, codeHighlighter, language, contents, dark, lines -> apply(view, lines));
    }
    private Binding bind(View view, Highlighter renderer, String path, String contents, boolean dark, Consumer<List<NativeSyntaxEngine.Line>> apply) {
        Binding binding = new Binding(view, renderer, path, contents, dark, apply);
        view.addOnAttachStateChangeListener(binding);
        if (view.isAttachedToWindow()) binding.onViewAttachedToWindow(view);
        return binding;
    }
    void decorateDiff(NativePatch.File file, List<NativeDiffLayout.Row> rows, boolean dark, int token) throws Exception {
        List<List<NativeDiffLayout.Cell>> groups = new java.util.ArrayList<>();
        for (int hunk = 0; hunk < file.hunks.size() * 2; hunk++) groups.add(new java.util.ArrayList<>());
        for (NativeDiffLayout.Row row : rows) {
            if (row.old != null && row.old.kind != 'n') groups.get(row.old.hunk * 2).add(row.old);
            if (row.next != null && row.next.kind != 'n') groups.get(row.next.hunk * 2 + 1).add(row.next);
        }
        for (int group = 0; group < groups.size(); group++) {
            List<NativeDiffLayout.Cell> cells = groups.get(group);
            StringBuilder text = new StringBuilder();
            if (cells.isEmpty()) continue;
            for (NativeDiffLayout.Cell cell : cells) text.append(cell.text);
            String path = group % 2 == 1 || file.prevName.isEmpty() ? file.name : file.prevName;
            List<NativeSyntaxEngine.Line> lines = compute(path, text.toString(), dark, token);
            for (int line = 0; line < cells.size() && line < lines.size(); line++)
                cells.get(line).syntax = renderRuns(cells.get(line).text, 0, lines.get(line).runs, dark ? 0xfffafafa : 0xff0a0a0a);
        }
    }
    final class Binding implements View.OnAttachStateChangeListener, AutoCloseable {
        final WeakReference<View> view;
        final Highlighter renderer;
        final String path;
        String contents;
        final boolean dark;
        final int token = epoch;
        final Consumer<List<NativeSyntaxEngine.Line>> apply;
        int attachment;
        boolean complete, disposed;
        Request pending;
        Binding(View view, Highlighter renderer, String path, String contents, boolean dark, Consumer<List<NativeSyntaxEngine.Line>> apply) {
            this.view = new WeakReference<>(view); this.renderer = renderer; this.path = path; this.contents = contents; this.dark = dark; this.apply = apply;
        }
        public void onViewAttachedToWindow(View view) {
            if (complete || !ownerCurrent()) return;
            pending = new Request(NativeSyntax.this, this, renderer, path, contents, dark, token, ++attachment);
            try { worker.execute(pending); }
            catch (java.util.concurrent.RejectedExecutionException ignored) { pending.cancel(false); pending = null; }
        }
        public void onViewDetachedFromWindow(View view) {
            ++attachment;
            if (pending != null) { pending.cancel(true); pending = null; }
        }
        boolean ownerCurrent() { return !disposed && current(token); }
        @Override public void close() {
            disposed = true; contents = null;
            View target = view.get();
            if (target != null) target.removeOnAttachStateChangeListener(this);
            onViewDetachedFromWindow(target);
        }
    }
    private static final class Request extends FutureTask<List<NativeSyntaxEngine.Line>> {
        final WeakReference<Binding> target;
        final int attachment;
        Request(NativeSyntax owner, Binding binding, Highlighter renderer, String path, String contents, boolean dark, int token, int attachment) {
            super(() -> owner.compute(renderer, path, contents, dark, token));
            target = new WeakReference<>(binding); this.attachment = attachment;
        }
        @Override protected void done() {
            if (isCancelled()) return;
            final List<NativeSyntaxEngine.Line> lines;
            try { lines = get(); }
            catch (java.util.concurrent.ExecutionException error) {
                if (!(error.getCause() instanceof java.util.concurrent.CancellationException))
                    android.util.Log.w("SpoolSyntax", "Source highlighting failed", error.getCause());
                return;
            } catch (InterruptedException error) { Thread.currentThread().interrupt(); return; }
            catch (java.util.concurrent.CancellationException ignored) { return; }
            new Handler(Looper.getMainLooper()).post(() -> {
                Binding binding = target.get();
                View view = binding == null ? null : binding.view.get();
                if (view == null || !view.isAttachedToWindow() || binding.attachment != attachment || !binding.ownerCurrent()) return;
                binding.apply.accept(lines); binding.complete = true; binding.contents = null; binding.pending = null;
            });
        }
    }

    void clear() { if (!closed) { ++epoch; worker.execute(cleanup); } }
    @Override public void close() { if (!closed) { closed = true; ++epoch; worker.execute(cleanup); } }

    static void apply(TextView view, List<NativeSyntaxEngine.Line> lines) {
        Spannable text = view.getText() instanceof Spannable ? (Spannable) view.getText() : new SpannableString(view.getText());
        apply(text, lines, view.getCurrentTextColor());
        if (text != view.getText()) view.setText(text, TextView.BufferType.SPANNABLE);
        view.invalidate();
    }
    private interface SyntaxSpan {}
    private static final class Color extends ForegroundColorSpan implements SyntaxSpan { Color(int value) { super(value); } }
    private static final class Style extends StyleSpan implements SyntaxSpan { Style(int value) { super(value); } }
    private static final class Underline extends UnderlineSpan implements SyntaxSpan {}
    private static final class Strike extends StrikethroughSpan implements SyntaxSpan {}
    static void clearSpans(CharSequence text) {
        if (text instanceof Spannable) for (SyntaxSpan span : ((Spannable) text).getSpans(0, text.length(), SyntaxSpan.class))
            ((Spannable) text).removeSpan(span);
    }
    static void apply(Spannable text, List<NativeSyntaxEngine.Line> lines, int foreground) {
        clearSpans(text);
        for (NativeSyntaxEngine.Line line : lines) for (NativeSyntaxEngine.Run run : renderRuns(text, line.offset, line.runs, foreground)) {
            int from = line.offset + run.start, end = line.offset + run.end;
            if (from < 0 || end > text.length() || end <= from) continue;
            if (run.color != foreground) text.setSpan(new Color(run.color), from, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            int style = typefaceStyle(run.style);
            if (style != Typeface.NORMAL) text.setSpan(new Style(style), from, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            if ((run.style & 4) != 0) text.setSpan(new Underline(), from, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            if ((run.style & 8) != 0) text.setSpan(new Strike(), from, end, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        }
    }
    static List<NativeSyntaxEngine.Run> renderRuns(CharSequence text, int offset, List<NativeSyntaxEngine.Run> tokens, int foreground) {
        // Pierre's renderTokenLines merges undecorated whitespace into the following token.
        List<NativeSyntaxEngine.Run> result = new java.util.ArrayList<>();
        int pending = -1;
        for (int i = 0; i < tokens.size(); i++) {
            NativeSyntaxEngine.Run token = tokens.get(i); boolean decorated = (token.style & 12) != 0;
            boolean whitespace = token.end > token.start;
            for (int at = token.start; whitespace && at < token.end; at++) whitespace = NativeWordDiff.whitespace(text.charAt(offset + at));
            if (!decorated && whitespace && i + 1 < tokens.size()) { if (pending < 0) pending = token.start; continue; }
            if (pending >= 0 && decorated) result.add(new NativeSyntaxEngine.Run(pending, token.start, foreground, 0));
            result.add(pending >= 0 && !decorated ? new NativeSyntaxEngine.Run(pending, token.end, token.color, token.style) : token);
            pending = -1;
        }
        return java.util.Collections.unmodifiableList(result);
    }
    static void draw(Canvas canvas, Paint paint, String text, List<NativeSyntaxEngine.Run> runs, float x, float baseline) {
        if (runs.isEmpty()) { NativeCodeTabs.draw(canvas, paint, text, x, baseline); return; }
        Typeface face = paint.getTypeface(); int color = paint.getColor();
        boolean underline = paint.isUnderlineText(), strike = paint.isStrikeThruText();
        try {
            for (NativeSyntaxEngine.Run run : runs) {
                paint.setTypeface(Typeface.create(face, typefaceStyle(run.style))); paint.setColor(run.color);
                paint.setUnderlineText((run.style & 4) != 0); paint.setStrikeThruText((run.style & 8) != 0);
                NativeCodeTabs.drawRange(canvas, paint, text, run.start, Math.min(text.length(), run.end), x, baseline);
            }
        } finally { paint.setTypeface(face); paint.setColor(color); paint.setUnderlineText(underline); paint.setStrikeThruText(strike); }
    }
    private static int typefaceStyle(int style) { return ((style & 1) != 0 ? Typeface.ITALIC : 0) | ((style & 2) != 0 ? Typeface.BOLD : 0); }
}
