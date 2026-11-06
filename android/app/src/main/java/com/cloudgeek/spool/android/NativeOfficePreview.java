package com.cloudgeek.spool.android;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.graphics.Typeface;
import android.os.Handler;
import android.os.Looper;
import android.text.SpannableStringBuilder;
import android.text.Spanned;
import android.text.style.BackgroundColorSpan;
import android.text.style.ForegroundColorSpan;
import android.text.style.StyleSpan;
import android.text.style.UnderlineSpan;
import android.view.Gravity;
import android.view.View;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;

/** Shared Go preview JSON to native Word blocks and paged Excel cells. */
final class NativeOfficePreview extends LinearLayout implements AutoCloseable {
    final LinearLayout toolbar;
    NativeSelect sheets;
    final NativeUi.IconButton previous, next;
    final TextView range, status;
    final ScrollView scroll;
    private final LinearLayout message;
    private final TextView notice;
    private final NativeUi ui;
    private final NativeFileBrowser.Read read;
    private final String path;
    private final Handler main = new Handler(Looper.getMainLooper());
    private JSONArray sheetList = new JSONArray();
    private int sheet, row, request;
    private boolean closed;

    NativeOfficePreview(NativeUi ui, NativeFileBrowser.Read read, String path) {
        super(ui.context); this.ui = ui; this.read = read; this.path = path; setOrientation(VERTICAL); setBackgroundColor(ui.elevated);
        toolbar = ui.row(); toolbar.setPadding(ui.dp(8), ui.dp(3), ui.dp(8), ui.dp(3)); toolbar.setVisibility(GONE);
        sheets = new NativeSelect(ui,ui.t("Worksheet"),"0",new String[0],new String[0],false,this::selectSheet); toolbar.addView(sheets, new LayoutParams(0, -2, 1));
        previous = ui.icon("chevron-left", 15, ui.t("Previous rows"), () -> load(sheet, Math.max(0, row-50)));
        next = ui.icon("chevron-right", 15, ui.t("Next rows"), () -> load(sheet, row+50));
        range = ui.text("", 11, ui.muted); toolbar.addView(previous); toolbar.addView(range); toolbar.addView(next); addView(toolbar);
        FrameLayout viewport = new FrameLayout(ui.context);
        scroll = new ScrollView(ui.context); viewport.addView(scroll, new FrameLayout.LayoutParams(-1, -1));
        status = NativeFilePreview.empty(ui, ui.t("Loading file…")); status.setGravity(Gravity.CENTER); status.setAccessibilityLiveRegion(ACCESSIBILITY_LIVE_REGION_POLITE);
        message = ui.column(); message.setGravity(Gravity.CENTER); message.addView(status);
        message.addView(ui.button(ui.t("Retry"), false, () -> load(sheet, row)));
        viewport.addView(message, new FrameLayout.LayoutParams(-1, -2, Gravity.CENTER)); addView(viewport, new LayoutParams(-1, 0, 1));
        notice = NativeFilePreview.notice(ui, ui.t("Preview truncated. Download the full file to see all content.")); notice.setVisibility(GONE); addView(notice);
        load(0, 0);
    }

    private void selectSheet(String value) { int index=Integer.parseInt(value); if (index!=sheet) load(index,0); }

    boolean closed() { return closed; }

    private void load(int selected, int start) {
        if (closed) return;
        sheet = selected; row = start; int pending = ++request;
        previous.setEnabled(false); next.setEnabled(false); sheets.setEnabled(false);
        scroll.removeAllViews(); message.setVisibility(VISIBLE); message.getChildAt(1).setVisibility(GONE);
        status.setText(ui.t("Loading file…")); status.setTextColor(ui.muted); notice.setVisibility(GONE);
        read.read(path + "&office=1&sheet=" + selected + "&row=" + start, (value, error) -> main.post(() -> {
            if (closed || pending != request) return;
            if (error != null || value == null || !(value.optString("kind").equals("word") || value.optString("kind").equals("spreadsheet"))) {
                status.setText(ui.t("This Office file cannot be previewed. Download it to open locally.")); status.setTextColor(ui.error);
                message.getChildAt(1).setVisibility(VISIBLE); return;
            }
            bind(value);
        }));
    }

    private void bind(JSONObject document) {
        message.setVisibility(GONE); notice.setVisibility(document.optBoolean("truncated") ? VISIBLE : GONE);
        if (document.optString("kind").equals("word")) {
            setContentDescription(ui.t("Word preview")); toolbar.setVisibility(GONE);
            scroll.setFillViewport(true);
            LinearLayout body = ui.column(); body.setBackgroundColor(Color.WHITE); body.setPadding(ui.dp(18), ui.dp(8), ui.dp(18), ui.dp(28));
            JSONArray blocks = document.optJSONArray("blocks");
            if (blocks != null) for (int i=0;i<blocks.length();i++) {
                JSONObject block = blocks.optJSONObject(i); if (block == null) continue;
                if (block.optString("kind").equals("table")) body.addView(NativeOfficeTable.scroll(ui, new NativeOfficeTable(ui, block, true)), new LayoutParams(-1, -2));
                else paragraph(body, block);
            }
            scroll.addView(body, new ScrollView.LayoutParams(-1, -2));
        } else {
            setContentDescription(ui.t("Excel preview")); toolbar.setVisibility(VISIBLE);
            scroll.setFillViewport(false);
            sheet = document.optInt("sheet"); row = document.optInt("row_start");
            sheetList = document.optJSONArray("sheets"); if (sheetList == null) sheetList = new JSONArray();
            String[] values=new String[sheetList.length()],names=new String[sheetList.length()];
            for (int i=0;i<sheetList.length();i++) { JSONObject s=sheetList.optJSONObject(i); names[i]=s.optString("name"); values[i]=String.valueOf(s.optInt("index")); }
            toolbar.removeView(sheets);
            sheets=new NativeSelect(ui,ui.t("Worksheet"),String.valueOf(sheet),values,names,false,this::selectSheet);
            toolbar.addView(sheets,0,new LayoutParams(0,-2,1));
            previous.setEnabled(row > 0); next.setEnabled(document.optBoolean("has_more"));
            JSONArray rows = document.optJSONArray("rows"); int count = rows == null ? 0 : rows.length();
            range.setText(count == 0 ? "" : ui.t("Rows {start}–{end}", "start", row+1, "end", row+count));
            if (count == 0) scroll.addView(NativeFilePreview.empty(ui, ui.t("This worksheet is empty.")));
            else scroll.addView(NativeOfficeTable.scroll(ui, new NativeOfficeTable(ui, document, false)), new ScrollView.LayoutParams(-1, -2));
        }
        scroll.scrollTo(0, 0);
    }

    private void paragraph(LinearLayout body, JSONObject block) {
        SpannableStringBuilder text = new SpannableStringBuilder(); JSONArray runs = block.optJSONArray("runs");
        if (runs != null) for (int i=0;i<runs.length();i++) {
            JSONObject run = runs.optJSONObject(i); if (run == null) continue;
            if (!run.optString("image").isEmpty()) {
                if (text.length() > 0) { text(body, text, block); text = new SpannableStringBuilder(); }
                try {
                    NativeFileData data = NativeFileData.inline(run.optString("image"));
                    BitmapFactory.Options options = new BitmapFactory.Options(); options.inJustDecodeBounds=true; BitmapFactory.decodeByteArray(data.bytes,0,data.bytes.length,options);
                    if (options.outWidth <= 0 || options.outHeight <= 0 || (long)options.outWidth*options.outHeight > 4_000_000) continue;
                    Bitmap bitmap = BitmapFactory.decodeByteArray(data.bytes,0,data.bytes.length);
                    NativeContainedImage image = new NativeContainedImage(ui,false); image.setContentDescription(run.optString("alt")); image.setImageBitmap(bitmap); body.addView(image,new LayoutParams(-2,-2));
                } catch (Exception ignored) { text.append(run.optString("alt")); }
                continue;
            }
            int start = text.length(); text.append(run.optString("text")); JSONObject s=run.optJSONObject("style"); if (s == null) continue;
            int flags = (s.optBoolean("bold") ? Typeface.BOLD : 0) | (s.optBoolean("italic") ? Typeface.ITALIC : 0);
            if (flags != 0) text.setSpan(new StyleSpan(flags),start,text.length(),Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            if (s.optBoolean("underline")) text.setSpan(new UnderlineSpan(),start,text.length(),Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            if (validColor(s.optString("color"))) text.setSpan(new ForegroundColorSpan(Color.parseColor(s.optString("color"))),start,text.length(),Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
            if (validColor(s.optString("background"))) text.setSpan(new BackgroundColorSpan(Color.parseColor(s.optString("background"))),start,text.length(),Spanned.SPAN_EXCLUSIVE_EXCLUSIVE);
        }
        if (text.length() > 0 || runs == null || runs.length() == 0) text(body,text,block);
    }

    private void text(LinearLayout body, CharSequence content, JSONObject block) {
        int level = block.optInt("level"); TextView view = ui.text("", level == 1 ? 23 : level == 2 ? 20 : level > 0 ? 17 : 14, 0xff202020);
        if (level > 0) view.setTypeface(view.getTypeface(),Typeface.BOLD);
        view.setText(content); view.setTextIsSelectable(true); ui.fixedLineHeight(view,1.6f); view.setGravity(gravity(block.optString("align")));
        view.setPadding(0,0,0,ui.dp(9)); body.addView(view,new LayoutParams(-1,-2));
    }

    static boolean validColor(String color) { return color.matches("#[0-9a-fA-F]{6}"); }
    private static int gravity(String align) { return Gravity.TOP | (align.equals("center") ? Gravity.CENTER_HORIZONTAL : align.equals("right") ? Gravity.RIGHT : Gravity.LEFT); }
    static void style(TextView text, JSONObject style) {
        if (style == null) return;
        boolean bold=style.optBoolean("bold"),italic=style.optBoolean("italic");
        text.setTypeface(text.getTypeface(),bold ? (italic ? Typeface.BOLD_ITALIC : Typeface.BOLD) : (italic ? Typeface.ITALIC : Typeface.NORMAL));
        if (style.optBoolean("underline")) text.setPaintFlags(text.getPaintFlags() | android.graphics.Paint.UNDERLINE_TEXT_FLAG);
        if (validColor(style.optString("color"))) text.setTextColor(Color.parseColor(style.optString("color")));
        if (validColor(style.optString("background"))) text.setBackgroundColor(Color.parseColor(style.optString("background")));
        text.setGravity(gravity(style.optString("align")));
    }

    @Override public void close() { if (closed) return; closed=true; request++; scroll.removeAllViews(); }
    @Override protected void onDetachedFromWindow() { close(); super.onDetachedFromWindow(); }
}
