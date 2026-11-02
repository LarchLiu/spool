package com.cloudgeek.spool.android;

import android.text.TextUtils;
import android.view.Gravity;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

/** FileSidebarHeader's native title/search/actions, with one CSS flex remainder. */
final class NativeFileHeader extends LinearLayout {
    final LinearLayout title;
    final NativeSearchField searchField;
    final EditText search;
    final NativeUi.IconButton searchToggle, refresh;
    private final NativeUi ui;

    NativeFileHeader(NativeUi ui, String name, Runnable toggle, Runnable reload, Runnable close) {
        super(ui.context);
        this.ui = ui;
        setOrientation(HORIZONTAL); setGravity(Gravity.CENTER_VERTICAL);
        // The browser owns the 1px left border; accumulate it with the 14px inset.
        setPadding(ui.dp(1 + 14) - ui.dp(1), 0, ui.dp(14), 0);
        setBackgroundColor(ui.sidebar); setClipChildren(false);
        title = ui.row(); title.addView(ui.decorativeIcon("folder-open", 16));
        TextView project = ui.bold(name.isEmpty() ? ui.t("Files") : name, 12);
        ui.typography(project, 12, ui.text, 700); project.setSingleLine(true); project.setEllipsize(TextUtils.TruncateAt.END);
        LayoutParams projectParams = new LayoutParams(0, -2, 1); projectParams.setMarginStart(ui.dp(8)); title.addView(project, projectParams);
        LayoutParams titleParams = new LayoutParams(0, -2, 1); titleParams.setMarginEnd(ui.dp(8)); addView(title, titleParams);
        searchField = new NativeSearchField(ui, ui.t("Search files"), ui.t("Clear file search")); search = searchField.input;
        LayoutParams searchParams = new LayoutParams(0, ui.dp(32)); searchParams.setMarginEnd(ui.dp(8)); addView(searchField, searchParams);
        searchToggle = ui.icon("search", 18, ui.t("Search files"), toggle); addView(searchToggle);
        refresh = ui.icon("refresh", 18, ui.t("Refresh files"), reload);
        LayoutParams action = new LayoutParams(ui.dp(32), ui.dp(32)); action.setMarginStart(ui.dp(2)); addView(refresh, action);
        LayoutParams collapse = new LayoutParams(ui.dp(32), ui.dp(32)); collapse.setMarginStart(ui.dp(4) - ui.dp(2));
        addView(ui.icon("files-close", 18, ui.t("Collapse file sidebar"), close), collapse);
    }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        if (searchField.getVisibility() != GONE) {
            LayoutParams flexible = (LayoutParams) searchField.getLayoutParams();
            // Round total fixed CSS length once, not each independent flex term.
            flexible.width = Math.max(0, MeasureSpec.getSize(widthSpec) - ui.dp(14 * 2 + 8 + 32 * 3 + 2 * 2));
        }
        super.onMeasure(widthSpec, heightSpec);
    }
}
