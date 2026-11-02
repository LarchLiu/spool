package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.text.Layout;
import android.view.ViewGroup;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;

/** Collapsed table borders and shared columns, without double-stroking adjacent cells. */
final class NativeMarkdownTable extends ViewGroup implements NativeMarkdownFlowBox {
    static final class Row extends ViewGroup {
        final List<TextView> cells = new ArrayList<>();
        float y, height;
        private final float density;
        private float roundingY;
        private float[] widths;
        Row(NativeUi ui) { super(ui.context); density = getResources().getDisplayMetrics().density; }
        void cell(TextView cell) { cells.add(cell); addView(cell); }
        void cells(List<TextView> next) {
            NativeKeyedViews.sync(this, next); cells.clear(); cells.addAll(next); requestLayout();
        }
        private float cellHeight(TextView cell) {
            // Collapsed cells own half of each 1px border plus 7px of CSS padding.
            return NativeUi.cssTextHeight(cell) - cell.getCompoundPaddingTop() - cell.getCompoundPaddingBottom() + 15 * density;
        }
        void measureColumns(float[] widths) {
            this.widths = widths;
            height = 0;
            float x = 0;
            for (int i = 0; i < cells.size(); i++) {
                TextView cell = cells.get(i);
                int width = Math.round(x + widths[i]) - Math.round(x);
                // Cell frames begin at the collapsed border center, before the 12px padding.
                int inset = Math.round(12.5f * density);
                int content = NativeCssWrap.width(cell, Math.max(0, widths[i] - 25 * density));
                cell.setPadding(inset, cell.getPaddingTop(), Math.max(0, width - inset - content), cell.getPaddingBottom());
                cell.measure(MeasureSpec.makeMeasureSpec(width, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(0, MeasureSpec.UNSPECIFIED));
                height = Math.max(height, cellHeight(cell));
                x += widths[i];
            }
            setMeasuredDimension(Math.round(x), Math.round(height));
        }
        @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
            layoutCells();
        }
        void layoutCells() {
            float x = 0;
            for (int i = 0; i < cells.size(); i++) {
                TextView cell = cells.get(i);
                int left = Math.round(x), right = Math.round(x + widths[i]);
                cell.measure(MeasureSpec.makeMeasureSpec(right - left, MeasureSpec.EXACTLY), MeasureSpec.makeMeasureSpec(getHeight(), MeasureSpec.EXACTLY));
                cell.layout(left, 0, right, getHeight());
                float textTop = roundingY + 7.5f * density + (height - cellHeight(cell)) / 2;
                NativeUi.cssTextRounding(cell, textTop - cell.getTotalPaddingTop());
                NativeUi.alignCssText(cell);
                x += widths[i];
            }
        }
    }

    private final List<Row> rows = new ArrayList<>();
    private final Paint border = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final float density;
    private float[] columns = new float[0];
    private int available;
    private float height;
    private float roundingY;
    private boolean roundingDirty;

    NativeMarkdownTable(NativeUi ui) {
        super(ui.context);
        density = getResources().getDisplayMetrics().density;
        border.setColor(ui.borderStrong); border.setStrokeWidth(density);
    }

    void row(Row row) { rows.add(row); addView(row); }
    void rows(List<Row> next) {
        NativeKeyedViews.sync(this, next); rows.clear(); rows.addAll(next); requestLayout();
    }
    void availableWidth(int width) { available = width; }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int count = 0;
        for (Row row : rows) count = Math.max(count, row.cells.size());
        columns = new float[count];
        java.util.Arrays.fill(columns, 90 * density);
        for (Row row : rows) for (int i = 0; i < row.cells.size(); i++) {
            TextView cell = row.cells.get(i);
            columns[i] = Math.max(columns[i], Layout.getDesiredWidth(cell.getText(), cell.getPaint()) + 25 * density);
        }
        float width = density;
        for (float column : columns) width += column;
        if (available > 0 && width > available && 90 * density * count + density <= available) {
            float flexible = width - density - 90 * density * count;
            float extra = available - density - 90 * density * count;
            for (int i = 0; i < count; i++) columns[i] = 90 * density + (columns[i] - 90 * density) * extra / flexible;
            width = available;
        }
        height = density;
        for (Row row : rows) {
            row.measureColumns(columns);
            row.y = height - density / 2;
            height += row.height;
        }
        setMeasuredDimension(Math.round(width), Math.round(height));
    }

    @Override protected void onLayout(boolean changed, int l, int t, int r, int b) {
        layoutRows();
    }

    private void layoutRows() {
        roundingDirty = false;
        for (Row row : rows) {
            int y = Math.round(row.y + roundingY), x = Math.round(density / 2);
            int bottom = Math.round(row.y + row.height + roundingY);
            row.roundingY = row.y + roundingY - y;
            row.layout(x, y, x + row.getMeasuredWidth(), bottom);
            row.layoutCells();
        }
    }

    @Override public void roundingOffset(float x, float y) {
        if (roundingY != y) { roundingY = y; roundingDirty = true; forceLayout(); }
    }

    @Override public void layoutRounding() { if (roundingDirty) layoutRows(); }

    @Override protected void dispatchDraw(Canvas canvas) {
        super.dispatchDraw(canvas);
        float edge = density / 2, x = edge;
        canvas.drawLine(edge, edge, getWidth() - edge, edge, border);
        for (Row row : rows) canvas.drawLine(edge, row.y + row.height, getWidth() - edge, row.y + row.height, border);
        canvas.drawLine(x, edge, x, height - edge, border);
        for (float column : columns) { x += column; canvas.drawLine(x, edge, x, height - edge, border); }
    }

    @Override public float cssHeight() { return height; }
}
