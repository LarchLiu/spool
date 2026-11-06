package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.view.View;
import android.view.ViewGroup;
import android.widget.HorizontalScrollView;
import android.widget.TextView;
import java.util.ArrayList;
import org.json.JSONArray;
import org.json.JSONObject;

/** Selectable native cells with shared column widths, wrapping and merged regions. */
final class NativeOfficeTable extends ViewGroup {
    private static final class Cell {
        final TextView view; final int row, col, rows, cols;
        Cell(TextView view, int row, int col, int rows, int cols) { this.view = view; this.row = row; this.col = col; this.rows = rows; this.cols = cols; }
    }
    private final NativeUi ui;
    private final boolean word;
    private final ArrayList<Cell> cells = new ArrayList<>();
    private final Paint border = new Paint();
    private final int[] widths, heights;
    private int[] xs, ys;
    private int available;

    NativeOfficeTable(NativeUi ui, JSONObject document, boolean word) {
        super(ui.context); this.ui = ui; this.word = word;
        setBackgroundColor(android.graphics.Color.WHITE); setWillNotDraw(false);
        border.setColor(0xffd8dadd); border.setStyle(Paint.Style.STROKE); border.setStrokeWidth(ui.dp(1));
        JSONArray rows = document.optJSONArray("rows"); if (rows == null) rows = new JSONArray();
        JSONArray columns = document.optJSONArray("columns"); int count = columns == null ? 0 : columns.length();
        if (word) for (int i = 0; i < rows.length(); i++) {
            JSONArray row = rows.optJSONArray(i); int size = 0;
            if (row != null) for (int j = 0; j < row.length(); j++) size += Math.max(1, row.optJSONObject(j).optInt("col_span", 1));
            count = Math.max(count, size);
        }
        count = Math.max(1, Math.min(128, count));
        widths = new int[count + (word ? 0 : 1)]; heights = new int[rows.length() + (word ? 0 : 1)];
        for (int i = 0; i < widths.length; i++) widths[i] = ui.dp(word ? 100 : i == 0 ? 48 : Math.max(64, Math.min(320, columns == null ? 100 : columns.optInt(i-1, 100))));
        if (!word) {
            add("", null, 0, 0, 1, 1, true);
            for (int i = 0; i < count; i++) add(column(i), null, 0, i+1, 1, 1, true);
        }
        for (int i = 0; i < rows.length(); i++) {
            int row = i + (word ? 0 : 1); JSONObject record = rows.optJSONObject(i);
            JSONArray data = word ? rows.optJSONArray(i) : record == null ? null : record.optJSONArray("cells");
            heights[row] = ui.dp(word || record == null ? 28 : Math.max(28, Math.min(320, record.optInt("height", 28))));
            if (!word) add(String.valueOf(record == null ? i+1 : record.optInt("number", i+1)), null, row, 0, 1, 1, true);
            int col = word ? 0 : 1;
            if (data != null) for (int j = 0; j < data.length() && col < widths.length; j++) {
                JSONObject value = data.optJSONObject(j); if (value == null) { col++; continue; }
                int span = Math.max(1, Math.min(widths.length-col, value.optInt("col_span", 1)));
                if (!value.optBoolean("hidden")) add(value.optString("text"), value.optJSONObject("style"), row, col,
                        Math.max(1, Math.min(heights.length-row, value.optInt("row_span", 1))), span, false);
                col += word ? span : 1;
            }
        }
    }

    static String column(int index) {
        String value = "";
        for (int n = index+1; n > 0; n = (n-1)/26) value = (char) ('A'+(n-1)%26) + value;
        return value;
    }

    private void add(String value, JSONObject style, int row, int col, int rows, int cols, boolean header) {
        TextView text = ui.text(value, 13, header ? 0xff555555 : 0xff202020);
        text.setTextIsSelectable(!header); ui.fixedLineHeight(text, 1.4f);
        text.setPadding(ui.dp(8), ui.dp(6), ui.dp(8), ui.dp(6));
        NativeOfficePreview.style(text, style);
        if (header) { text.setBackgroundColor(0xfff1f2f3); text.setGravity(android.view.Gravity.CENTER); }
        cells.add(new Cell(text, row, col, rows, cols)); addView(text);
    }

    static HorizontalScrollView scroll(NativeUi ui, NativeOfficeTable table) {
        HorizontalScrollView scroll = new HorizontalScrollView(ui.context) {
            @Override protected void onMeasure(int width, int height) { table.available = MeasureSpec.getSize(width); super.onMeasure(width, height); }
        };
        scroll.setFillViewport(true); scroll.addView(table, new HorizontalScrollView.LayoutParams(-2, -2)); return scroll;
    }

    private static int[] positions(int[] sizes) { int[] result = new int[sizes.length+1]; for (int i=0;i<sizes.length;i++) result[i+1] = result[i]+sizes[i]; return result; }

    @Override protected void onMeasure(int width, int height) {
        if (word) for (int i=0;i<widths.length;i++) widths[i] = Math.max(ui.dp(100), available / widths.length);
        xs = positions(widths);
        for (int i=0;i<heights.length;i++) heights[i] = Math.max(ui.dp(28), heights[i]);
        for (Cell cell : cells) {
            cell.view.measure(MeasureSpec.makeMeasureSpec(xs[cell.col+cell.cols]-xs[cell.col], MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
            if (cell.rows == 1) heights[cell.row] = Math.max(heights[cell.row], cell.view.getMeasuredHeight());
        }
        for (Cell cell : cells) if (cell.rows > 1) {
            int total=0; for (int i=cell.row;i<cell.row+cell.rows;i++) total+=heights[i];
            if (cell.view.getMeasuredHeight() > total) heights[cell.row+cell.rows-1] += cell.view.getMeasuredHeight()-total;
        }
        ys = positions(heights);
        setMeasuredDimension(resolveSize(xs[xs.length-1], width), resolveSize(ys[ys.length-1], height));
    }

    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        for (Cell cell : cells) cell.view.layout(xs[cell.col], ys[cell.row], xs[cell.col+cell.cols], ys[cell.row+cell.rows]);
    }
    @Override protected void dispatchDraw(Canvas canvas) {
        super.dispatchDraw(canvas);
        for (Cell cell : cells) canvas.drawRect(new RectF(cell.view.getLeft()+.5f, cell.view.getTop()+.5f, cell.view.getRight()-.5f, cell.view.getBottom()-.5f), border);
    }
}
