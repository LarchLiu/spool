package com.cloudgeek.spool.android;

import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.drawable.Drawable;
import android.text.Editable;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.function.Consumer;
import org.json.JSONObject;

/** Web mobile ProjectFiles: lazy directory tree, loaded-entry search and inline resizable preview. */
final class NativeFileBrowser extends LinearLayout {
    interface Completion { void done(JSONObject value, Exception error); }
    interface Read { void read(String relativeAPIPath, Completion completion); }
    final NativeFileModel state;
    final NativeFileHeader header;
    final LinearLayout treePane, notices;
    final NativeFileSplit split;
    final ListView list;
    final TextView empty;
    final NativeSpinner loading;
    final EditText search;
    final NativeSearchField searchField;
    final NativeUi.IconButton searchToggle;
    final NativeFilePreview preview;
    private final NativeUi ui;
    private final Read read;
    private final LinearLayout title;
    private final FrameLayout treeSection;
    private final Paint borderPaint = new Paint();
    private final Set<String> pending = new LinkedHashSet<>();
    private List<NativeFileModel.Row> rows = new ArrayList<>();
    private boolean active = true, syncingSearch, started, workspacePending;
    private int workspaceRequest;
    private final Runnable workspacePoll = this::refreshWorkspace;
    private String revealing = "";
    private final BaseAdapter adapter = new BaseAdapter() {
        @Override public int getCount() { return rows.size(); }
        @Override public Object getItem(int position) { return rows.get(position); }
        @Override public long getItemId(int position) { return position; }
        @Override public View getView(int position, View reusable, ViewGroup parent) {
            Row row = reusable instanceof Row ? (Row) reusable : new Row();
            row.bind(rows.get(position), position); return row;
        }
    };

    NativeFileBrowser(NativeUi ui, String name, NativeFileModel state, NativeImages images, Read read,
            Consumer<JSONObject> download, Consumer<String> external, Runnable close, java.util.function.DoubleConsumer resize) {
        this(ui, name, state, images, null, read, download, external, close, resize);
    }

    NativeFileBrowser(NativeUi ui, String name, NativeFileModel state, NativeImages images, NativePdfPreview.Factory pdfFactory, Read read,
            Consumer<JSONObject> download, Consumer<String> external, Runnable close, java.util.function.DoubleConsumer resize) {
        super(ui.context);
        this.ui = ui; this.state = state; this.read = read;
        setOrientation(VERTICAL); setBackgroundColor(ui.elevated); setWillNotDraw(false);
        setPadding(ui.dp(1), 0, 0, 0);
        setContentDescription(ui.t("Project files"));
        header = new NativeFileHeader(ui, name, () -> toggleSearch(!state.searchOpen), this::reload, close);
        title = header.title; searchField = header.searchField; search = header.search; searchToggle = header.searchToggle;
        addView(header, new LayoutParams(-1, ui.dp(49)));
        addView(ui.divider(), new LayoutParams(-1, ui.dp(50) - ui.dp(49)));
        treePane = ui.column(); treePane.setPadding(0, ui.dp(58) - ui.dp(50), 0, 0);
        treeSection = new FrameLayout(ui.context);
        list = new ListView(ui.context); list.setDivider(null); list.setDividerHeight(0); list.setVerticalScrollBarEnabled(false);
        list.setSelector(new android.graphics.drawable.ColorDrawable(0)); list.setCacheColorHint(0); list.setPadding(ui.dp(14), 0, ui.dp(14), 0); list.setClipToPadding(false);
        list.setContentDescription(ui.t("Project file tree")); list.setAdapter(adapter);
        list.setOnItemClickListener((parent, view, position, id) -> activate(position));
        list.setOnKeyListener((view, code, event) -> key(code, event));
        list.setOnFocusChangeListener((view, focused) -> adapter.notifyDataSetChanged());
        treeSection.addView(list, new FrameLayout.LayoutParams(-1, -1));
        empty = NativeFilePreview.empty(ui, ui.t("This directory is empty.")); treeSection.addView(empty, new FrameLayout.LayoutParams(-1, -2));
        loading = new NativeSpinner(ui, 14, ui.t("Loading files…"));
        FrameLayout.LayoutParams progress = new FrameLayout.LayoutParams(ui.dp(14), ui.dp(14), Gravity.BOTTOM | Gravity.END); progress.setMarginEnd(ui.dp(12)); progress.bottomMargin = ui.dp(10);
        treeSection.addView(loading, progress);
        treePane.addView(treeSection, new LayoutParams(-1, 0, 1)); notices = ui.column(); treePane.addView(notices);
        split = new NativeFileSplit(ui, treePane, state.split, value -> { state.split = value; resize.accept(value); });
        addView(split, new LayoutParams(-1, 0, 1));
        preview = new NativeFilePreview(ui, state.project, state.chat, images, pdfFactory, this::select,
                address -> { if (active) external.accept(address); }, file -> { if (active) download.accept(file); }, this::clearPreview);
        search.addTextChangedListener(new TextWatcher() {
            public void beforeTextChanged(CharSequence value, int start, int count, int after) {}
            public void onTextChanged(CharSequence value, int start, int before, int count) { if (!syncingSearch) { state.search(value.toString()); renderTree(); loadExpanded(); } }
            public void afterTextChanged(Editable value) {}
        });
        search.setOnKeyListener((view, key, event) -> {
            if (key == KeyEvent.KEYCODE_ESCAPE && event.getAction() == KeyEvent.ACTION_DOWN) { toggleSearch(false); return true; }
            return false;
        });
        bindSearch(); renderTree(); bindPreview();
        if (state.project.isEmpty()) {
            searchToggle.setVisibility(GONE); header.refresh.setVisibility(GONE);
            treePane.setPadding(0, 0, 0, 0);
            empty.setText(ui.t("Choose a project to browse its files."));
        }
    }

    void start() {
        if (state.project.isEmpty() || started) return;
        active = true; started = true;
        if (!state.chat.isEmpty()) refreshWorkspace();
        else { loadDirectory("."); loadExpanded(); }
        if (state.target != null) select(state.target);
    }
    void resume() { if (!active) { started = false; start(); } else refreshWorkspace(); }
    void deactivate() {
        active = false; state.epoch++; state.fileRequest++;
        preview.releasePdf();
        workspaceRequest++; workspacePending = false; removeCallbacks(workspacePoll);
        header.workspaceInfo.popover.dismiss();
        state.requested.removeAll(pending); pending.clear();
        hideKeyboard();
    }
    void toggleSearch(boolean open) {
        if (open) state.beginSearch(); else state.closeSearch();
        syncingSearch = true; search.setText(""); syncingSearch = false;
        bindSearch(); renderTree();
        if (open) {
            search.requestFocus();
            ((android.view.inputmethod.InputMethodManager) ui.context.getSystemService(android.content.Context.INPUT_METHOD_SERVICE)).showSoftInput(search, android.view.inputmethod.InputMethodManager.SHOW_IMPLICIT);
        } else { hideKeyboard(); searchToggle.requestFocus(); }
    }
    private void hideKeyboard() { ((android.view.inputmethod.InputMethodManager) ui.context.getSystemService(android.content.Context.INPUT_METHOD_SERVICE)).hideSoftInputFromWindow(search.getWindowToken(), 0); }
    private void bindSearch() {
        title.setVisibility(state.searchOpen ? GONE : VISIBLE); searchField.setVisibility(state.searchOpen ? VISIBLE : GONE); search.setVisibility(state.searchOpen ? VISIBLE : GONE);
        searchToggle.setKind(state.searchOpen ? "close" : "search"); searchToggle.setContentDescription(state.searchOpen ? ui.t("Close file search") : ui.t("Search files"));
        if (!search.getText().toString().equals(state.query)) { syncingSearch = true; search.setText(state.query); syncingSearch = false; }
    }

    void reload() {
        if (!active) return;
        state.reset(); pending.clear(); revealing = "";
        refreshWorkspace(); loadDirectory(".");
        if (state.target != null) select(state.target);
        renderTree();
    }

    void refreshWorkspace() {
        if (!active || state.chat.isEmpty() || workspacePending) return;
        removeCallbacks(workspacePoll);
        workspacePending = true;
        int request = ++workspaceRequest;
        read.read(state.apiBase() + "/workspace", (value, error) -> {
            if (!active || request != workspaceRequest) return;
            workspacePending = false;
            if (error == null && (value == null || value.optString("path").isEmpty())) error = new java.io.IOException(ui.t("Session directory is unavailable"));
            JSONObject previous = state.workspace;
            boolean wasReady = state.ready();
            String oldPath = previous == null ? "" : previous.optString("path");
            String newPath = error == null ? value.optString("path") : "";
            state.workspace = error == null ? value : null;
            state.workspaceError = error == null ? "" : error.getMessage() == null ? ui.t("Request failed") : error.getMessage();
            if (!oldPath.equals(newPath) || wasReady != state.ready()) {
                NativeFileTarget target = previous == null || oldPath.equals(newPath) ? state.target : null;
                state.clearScope(); pending.clear(); revealing = "";
                bindSearch(); bindPreview();
                if (target != null) select(target);
            }
            if (state.ready()) {
                loadDirectory("."); loadExpanded();
                if (state.file != null && !state.file.optBoolean("external")) reveal(state.file.optString("path"));
            }
            renderTree();
            postDelayed(workspacePoll, 10_000);
        });
    }
    private void loadExpanded() { for (String path : new ArrayList<>(state.expanded)) loadDirectory(path); }
    void loadDirectory(String input) {
        String path = NativeFileModel.key(input);
        if (!active || !state.ready() || state.requested.contains(path)) return;
        state.requested.add(path); pending.add(path); renderTree();
        int epoch = state.epoch;
        read.read(state.apiBase() + "/files?path=" + android.net.Uri.encode(path), (value, error) -> {
            if (!active || epoch != state.epoch) return;
            pending.remove(path);
            if (error != null) state.error = error.getMessage() == null ? ui.t("Request failed") : error.getMessage();
            else state.accept(path, value);
            renderTree();
            if (!revealing.isEmpty()) reveal(revealing);
            loadExpanded();
        });
    }

    void select(NativeFileTarget target) {
        if (!active) return;
        state.target = target; state.selected = target.path; state.fileError = ""; revealing = "";
        int request = ++state.fileRequest, epoch = state.epoch;
        bindPreview(); renderTree();
        read.read(state.filePath(target.path, ""), (value, error) -> {
            if (!active || epoch != state.epoch || request != state.fileRequest) return;
            if (error != null) { state.file = null; state.fileError = error.getMessage() == null ? ui.t("Request failed") : error.getMessage(); }
            else {
                state.file = value; state.loadedTarget = target;
                if (!value.optBoolean("external") && state.ready()) { loadDirectory("."); revealing = value.optString("path"); reveal(revealing); }
            }
            bindPreview(); renderTree();
        });
    }
    private void reveal(String path) {
        state.selected = path;
        Set<String> ancestors = new LinkedHashSet<>(); NativeFileModel.ancestors(path, ancestors);
        for (String directory : ancestors) {
            state.expanded.add(directory);
            if (!state.directories.containsKey(directory)) { loadDirectory(directory); return; }
        }
        revealing = "";
        state.focused = path;
        list.post(() -> { if (!active) return; for (int i = 0; i < rows.size(); i++) if (rows.get(i).entry.path.equals(path)) { list.smoothScrollToPosition(i); break; } });
    }
    private void clearPreview() { state.target = null; state.file = null; state.loadedTarget = null; state.selected = ""; state.fileError = ""; state.fileRequest++; revealing = ""; bindPreview(); renderTree(); }
    private void bindPreview() {
        if (state.target == null) { preview.releasePdf(); split.preview(null); return; }
        String imageScope = (state.workspace == null ? "" : state.workspace.optString("path")) + "\n" + state.epoch;
        preview.bind(state.file, state.file == null ? state.target : state.loadedTarget, state.fileError, imageScope);
        if (split.preview != preview) split.preview(preview);
    }

    private void activate(int position) {
        if (position < 0 || position >= rows.size()) return;
        NativeFileModel.Row row = rows.get(position); state.focused = row.entry.path;
        if (row.entry.directory) {
            if (!state.expanded.remove(row.entry.path)) { state.expanded.add(row.entry.path); loadDirectory(row.entry.path); }
            renderTree();
        } else select(new NativeFileTarget(row.entry.path, 0, 0));
    }
    private boolean key(int code, KeyEvent event) {
        if (event.getAction() != KeyEvent.ACTION_DOWN || rows.isEmpty()) return false;
        int position = 0;
        for (int i = 0; i < rows.size(); i++) if (rows.get(i).entry.path.equals(state.focused)) position = i;
        NativeFileModel.Row row = rows.get(position);
        if (code == KeyEvent.KEYCODE_DPAD_DOWN) position = Math.min(rows.size() - 1, position + 1);
        else if (code == KeyEvent.KEYCODE_DPAD_UP) position = Math.max(0, position - 1);
        else if (code == KeyEvent.KEYCODE_MOVE_HOME) position = 0;
        else if (code == KeyEvent.KEYCODE_MOVE_END) position = rows.size() - 1;
        else if (code == KeyEvent.KEYCODE_ENTER || code == KeyEvent.KEYCODE_SPACE) { activate(position); return true; }
        else if (code == KeyEvent.KEYCODE_DPAD_RIGHT && row.entry.directory) { if (!row.expanded) activate(position); else if (position + 1 < rows.size()) position++; }
        else if (code == KeyEvent.KEYCODE_DPAD_LEFT) {
            if (row.expanded) { activate(position); return true; }
            for (int i = position - 1; i >= 0; i--) if (rows.get(i).depth < row.depth) { position = i; break; }
        } else return false;
        state.focused = rows.get(position).entry.path; list.setSelection(position); adapter.notifyDataSetChanged(); return true;
    }

    void renderTree() {
        header.workspace(state);
        rows = state.rows();
        String candidate = state.focused;
        while (!candidate.isEmpty() && !containsRow(candidate)) {
            int slash = candidate.lastIndexOf('/');
            candidate = slash < 0 ? "" : candidate.substring(0, slash);
        }
        state.focused = candidate.isEmpty() && !rows.isEmpty() ? rows.get(0).entry.path : candidate;
        adapter.notifyDataSetChanged(); loading.setVisibility(pending.isEmpty() ? GONE : VISIBLE);
        empty.setVisibility(state.ready() && pending.isEmpty() && state.directories.values().stream().allMatch(List::isEmpty) && state.error.isEmpty() ? VISIBLE : GONE);
        notices.removeAllViews();
        if (!state.ready()) {
            String message = !state.workspaceError.isEmpty() ? state.workspaceError : state.workspace == null ? ui.t("Loading session directory…") : ui.t("Session directory is unavailable");
            TextView notice = NativeFilePreview.notice(ui, message + (state.workspaceError.isEmpty() ? "" : "  " + ui.t("Retry")));
            if (!state.workspaceError.isEmpty()) { notice.setFocusable(true); notice.setOnClickListener(view -> reload()); }
            notices.addView(notice);
        }
        if (!state.error.isEmpty()) {
            TextView notice = NativeFilePreview.notice(ui, state.error + "  " + ui.t("Retry")); notice.setFocusable(true); notice.setOnClickListener(view -> reload()); notices.addView(notice);
        }
        if (state.truncated) notices.addView(NativeFilePreview.notice(ui, ui.t("Large directories show up to 2,000 entries.")));
    }

    private boolean containsRow(String path) {
        for (NativeFileModel.Row row : rows) if (row.entry.path.equals(path)) return true;
        return false;
    }

    private final class Row extends NativeTreeRow {
        NativeFileModel.Row data;
        Drawable icon;
        Row() { super(ui); setSingleLine(true); setGravity(Gravity.CENTER_VERTICAL); setLayoutParams(new android.widget.AbsListView.LayoutParams(-1, ui.dp(30))); }
        void bind(NativeFileModel.Row data, int position) {
            this.data = data;
            float height = 30 * getResources().getDisplayMetrics().density;
            ViewGroup.LayoutParams params = getLayoutParams();
            // Recycled rows retain their cumulative CSS edge, not 79px for every 78.75px row.
            int pixels = Math.round((position + 1) * height) - Math.round(position * height);
            if (params.height != pixels) { params.height = pixels; setLayoutParams(params); }
            // Pierre's rendered filenames stay regular even while filtering search results.
            ui.typography(this, 13, ui.text, 400); ui.cssLineHeight(this, 30f / 13); setText(data.entry.name);
            setPadding(ui.dp(32 + data.depth * 24), 0, ui.dp(10), 0);
            boolean selected = state.selected.equals(data.entry.path);
            setTreeState(selected, (state.searchOpen || list.hasFocus()) && state.focused.equals(data.entry.path));
            setContentDescription(data.entry.path + (data.entry.directory ? "/" : ""));
            icon = NativeIcons.drawable(ui.context, data.entry.directory ? "tree-chevron" : NativeTreeIcons.file(data.entry.path), selected && data.entry.directory ? ui.text : 0xff84848a, ui.dp(16));
            if (android.os.Build.VERSION.SDK_INT >= 30) setStateDescription(data.entry.directory ? data.expanded ? ui.label("Expanded", "已展开") : ui.label("Collapsed", "已折叠") : selected ? ui.label("Selected", "已选择") : "");
            invalidate();
        }
        @Override protected void drawLeading(Canvas canvas) {
            if (data == null) return;
            int left = ui.dp(10 + data.depth * 24);
            float top = (getHeight() - ui.dp(16)) / 2f;
            canvas.save(); canvas.translate(left, top);
            if (data.entry.directory && !data.expanded) canvas.rotate(-90, ui.dp(8), ui.dp(8));
            icon.draw(canvas); canvas.restore();
        }
    }

    @Override protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        borderPaint.setColor(ui.line); canvas.drawRect(0, 0, ui.dp(1), getHeight(), borderPaint);
    }
}
