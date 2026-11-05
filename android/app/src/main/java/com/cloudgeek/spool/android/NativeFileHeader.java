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
    final NativeUi.IconButton searchToggle, refresh, info;
    final NativeSessionWorkspace workspaceInfo;
    private final NativeUi ui;
    private final TextView project;

    NativeFileHeader(NativeUi ui, String name, Runnable toggle, Runnable reload, Runnable close) {
        super(ui.context);
        this.ui = ui;
        setOrientation(HORIZONTAL); setGravity(Gravity.CENTER_VERTICAL);
        // The browser owns the 1px left border; accumulate it with the 14px inset.
        setPadding(ui.dp(1 + 14) - ui.dp(1), 0, ui.dp(14), 0);
        setBackgroundColor(ui.sidebar); setClipChildren(false);
        title = new LinearLayout(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                measureTitle(widthSpec);
                super.onMeasure(widthSpec, heightSpec);
            }
        };
        title.setOrientation(HORIZONTAL); title.setGravity(Gravity.CENTER_VERTICAL);
        title.addView(ui.decorativeIcon("folder-open", 16));
        project = ui.bold(name.isEmpty() ? ui.t("Files") : name, 12);
        ui.typography(project, 12, ui.text, 700); project.setSingleLine(true); project.setEllipsize(TextUtils.TruncateAt.END);
        LayoutParams projectParams = new LayoutParams(-2, -2); projectParams.setMarginStart(ui.dp(8)); title.addView(project, projectParams);
        workspaceInfo = new NativeSessionWorkspace(ui);
        info = ui.icon("info", 14, ui.t("Session directory"), this::showWorkspace);
        info.setMinimumWidth(0); info.setMinimumHeight(0); info.setVisibility(GONE);
        info.setPadding(ui.dp(4), ui.dp(5), ui.dp(4), ui.dp(5));
        LayoutParams infoParams = new LayoutParams(ui.dp(22), ui.dp(24)); infoParams.setMarginStart(ui.dp(8)); title.addView(info, infoParams);
        LayoutParams titleParams = new LayoutParams(0, -2, 1); titleParams.setMarginEnd(ui.dp(8)); addView(title, titleParams);
        searchField = new NativeSearchField(ui, ui.t("Search files"), ui.t("Clear file search")); search = searchField.input;
        LayoutParams searchParams = new LayoutParams(0, ui.dp(32)); searchParams.setMarginEnd(ui.dp(8)); addView(searchField, searchParams);
        searchToggle = ui.icon("search", 18, ui.t("Search files"), toggle); addView(searchToggle);
        refresh = ui.icon("refresh", 18, ui.t("Refresh files"), reload);
        LayoutParams action = new LayoutParams(ui.dp(32), ui.dp(32)); action.setMarginStart(ui.dp(2)); addView(refresh, action);
        LayoutParams collapse = new LayoutParams(ui.dp(32), ui.dp(32)); collapse.setMarginStart(ui.dp(4) - ui.dp(2));
        addView(ui.icon("files-close", 18, ui.t("Collapse file sidebar"), close), collapse);
    }

    private void showWorkspace() { workspaceInfo.popover.show(info, false); }

    private void measureTitle(int widthSpec) {
        project.setMaxWidth(Math.max(0, MeasureSpec.getSize(widthSpec) - ui.dp(16 + 8 + (info.getVisibility() == GONE ? 0 : 8 + 22))));
    }

    void workspace(NativeFileModel state) {
        info.setVisibility(state.chat.isEmpty() ? GONE : VISIBLE);
        workspaceInfo.bind(state.workspace, state.workspaceError);
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
