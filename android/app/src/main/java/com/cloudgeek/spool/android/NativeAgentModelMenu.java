package com.cloudgeek.spool.android;

import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.SeekBar;
import android.widget.TextView;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;
import org.json.JSONArray;
import org.json.JSONObject;

/** Reusable native counterpart of Web AgentModelMenu, including an independently scrolling catalog. */
final class NativeAgentModelMenu extends LinearLayout {
    private final NativeUi ui;
    private JSONArray agents;
    private final String currentBackend;
    private final Consumer<NativeModelSelection> change;
    private NativeModelSelection selection;
    final ScrollView catalog;

    NativeAgentModelMenu(NativeUi ui, JSONArray agents, NativeModelSelection selection,
            String currentBackend, Consumer<NativeModelSelection> change) {
        super(ui.context);
        this.ui = ui;
        this.agents = agents;
        this.selection = selection;
        this.currentBackend = currentBackend;
        this.change = change;
        setOrientation(VERTICAL);
        setPadding(ui.dp(15), ui.dp(17), ui.dp(15), ui.dp(15));
        setContentDescription(ui.t("Choose agent and model"));
        catalog = new ScrollView(ui.context) {
            @Override protected void onMeasure(int widthSpec, int heightSpec) {
                super.onMeasure(widthSpec, MeasureSpec.makeMeasureSpec(ui.dp(240), MeasureSpec.AT_MOST));
            }
        };
        catalog.setVerticalScrollBarEnabled(false);
        catalog.setNestedScrollingEnabled(true);
        catalog.setOnTouchListener((view, event) -> {
            if (event.getActionMasked() == MotionEvent.ACTION_DOWN) view.getParent().requestDisallowInterceptTouchEvent(true);
            else if (event.getActionMasked() == MotionEvent.ACTION_UP || event.getActionMasked() == MotionEvent.ACTION_CANCEL)
                view.getParent().requestDisallowInterceptTouchEvent(false);
            return false;
        });
        render();
    }

    static String effortLabel(NativeUi ui, String value) {
        switch (value) {
            case "none": return ui.t("None");
            case "minimal": return ui.t("Minimal");
            case "low": return ui.t("Low");
            case "medium": return ui.t("Medium");
            case "high": return ui.t("High");
            case "xhigh": return ui.t("Extra High");
            case "max": return ui.t("Max");
            default: return value.isEmpty() ? ui.t("Default") : value;
        }
    }

    void bind(NativeModelSelection value) { selection = value; render(); }

    void bindAgents(JSONArray value) { agents = value; render(); }

    private void render() {
        int scrollY = catalog.getScrollY();
        removeAllViews();
        catalog.removeAllViews();
        LinearLayout groups = ui.column();
        JSONObject chosen = new JSONObject();
        for (int i = 0; i < agents.length(); i++) {
            JSONObject agent = agents.optJSONObject(i);
            if (agent == null) continue;
            String backend = agent.optString("backend");
            if (backend.equals(selection.backend)) chosen = NativeModelSelection.model(agent, selection.model);
            if (i > 0) {
                LinearLayout.LayoutParams border = new LinearLayout.LayoutParams(-1, ui.dp(1));
                border.topMargin = ui.dp(10);
                border.bottomMargin = ui.dp(10);
                groups.addView(ui.divider(), border);
            }
            LinearLayout heading = ui.row();
            heading.setPadding(ui.dp(7), 0, ui.dp(7), 0);
            heading.addView(ui.decorativeIcon(backend.equals("claude") ? "claude" : "codex", 14));
            TextView label = ui.text(backend.equals("claude") ? "Claude" : "Codex", 11, ui.muted);
            ui.typography(label, 11, ui.muted, 700);
            LinearLayout.LayoutParams title = new LinearLayout.LayoutParams(0, -2, 1);
            title.setMarginStart(ui.dp(7));
            heading.addView(label, title);
            boolean available = agent.optBoolean("enabled") && agent.optBoolean("installed");
            heading.addView(ui.text(!agent.optBoolean("enabled") ? ui.t("Disabled")
                    : !agent.optBoolean("installed") ? ui.t("Not installed") : "", 11, ui.muted));
            LinearLayout.LayoutParams headingParams = new LinearLayout.LayoutParams(-1, -2);
            headingParams.bottomMargin = ui.dp(7);
            groups.addView(heading, headingParams);
            List<JSONObject> models = new ArrayList<>();
            JSONObject data = agent.optJSONObject("catalog");
            JSONArray entries = data == null ? null : data.optJSONArray("models");
            boolean discovered = false;
            if (entries != null) for (int j = 0; j < entries.length(); j++) {
                JSONObject entry = entries.optJSONObject(j);
                if (entry == null) continue;
                discovered |= entry.optString("model").equals(selection.model);
                models.add(entry);
            }
            if (backend.equals(selection.backend) && !selection.model.isEmpty() && !discovered)
                models.add(0, NativeModelSelection.entry(selection.model));
            if (models.isEmpty()) models.add(new JSONObject());
            for (JSONObject entry : models) {
                String id = entry.optString("model");
                boolean active = backend.equals(selection.backend) && id.equals(selection.model);
                LinearLayout row = ui.row();
                row.setPadding(ui.dp(28), ui.dp(8), ui.dp(8), ui.dp(8));
                row.setBackground(ui.states(active ? ui.selected : 0, 7, 0, ui.selected, 0));
                TextView name = ui.text(entry.optString("displayName", id.isEmpty() ? ui.t("CLI default") : id), 12, ui.text);
                row.addView(name, new LinearLayout.LayoutParams(0, -2, 1));
                if (active) {
                    LinearLayout.LayoutParams check = new LinearLayout.LayoutParams(ui.dp(14), ui.dp(14));
                    check.setMarginStart(ui.dp(10));
                    row.addView(ui.decorativeIcon("check", 14), check);
                }
                row.setContentDescription(name.getText());
                row.setFocusable(true);
                row.setSelected(active);
                row.setEnabled(available);
                row.setAlpha(available ? 1 : .45f);
                row.setOnClickListener(view -> {
                    selection = NativeModelSelection.choose(agent, id);
                    change.accept(selection);
                    render();
                });
                groups.addView(row, new LinearLayout.LayoutParams(-1, -2));
            }
        }
        catalog.addView(groups);
        addView(catalog, new LinearLayout.LayoutParams(-1, -2));
        catalog.post(() -> catalog.scrollTo(0, scrollY));
        List<String> levels = NativeModelSelection.levels(selection.backend, chosen);
        if (!levels.isEmpty()) {
            LinearLayout.LayoutParams border = new LinearLayout.LayoutParams(-1, ui.dp(1));
            border.topMargin = ui.dp(13);
            border.bottomMargin = ui.dp(13);
            addView(ui.divider(), border);
            TextView label = ui.text(effortLabel(ui, selection.effort), 13, 0xff2778b9);
            ui.typography(label, 13, 0xff2778b9, 700);
            label.setGravity(Gravity.CENTER);
            addView(label, new LinearLayout.LayoutParams(-1, -2));
            int selected = Math.max(0, levels.indexOf(selection.effort));
            NativeReasoningSlider slider = new NativeReasoningSlider(ui, levels.size(), selected);
            slider.setOnSeekBarChangeListener(new SeekBar.OnSeekBarChangeListener() {
                @Override public void onProgressChanged(SeekBar view, int progress, boolean fromUser) {
                    if (!fromUser) return;
                    selection = selection.withEffort(levels.get(progress));
                    label.setText(effortLabel(ui, selection.effort));
                    if (android.os.Build.VERSION.SDK_INT >= 30) view.setStateDescription(label.getText());
                    change.accept(selection);
                }
                @Override public void onStartTrackingTouch(SeekBar view) { view.getParent().requestDisallowInterceptTouchEvent(true); }
                @Override public void onStopTrackingTouch(SeekBar view) { view.getParent().requestDisallowInterceptTouchEvent(false); }
            });
            if (android.os.Build.VERSION.SDK_INT >= 30) slider.setStateDescription(label.getText());
            LinearLayout.LayoutParams sliderParams = new LinearLayout.LayoutParams(-1, ui.dp(26));
            sliderParams.topMargin = ui.dp(18);
            sliderParams.bottomMargin = ui.dp(12);
            sliderParams.setMarginStart(ui.dp(1));
            sliderParams.setMarginEnd(ui.dp(1));
            addView(slider, sliderParams);
            LinearLayout captions = ui.row();
            captions.addView(ui.text(ui.t(selection.backend.equals("claude") ? "Default" : "Less"), 9, ui.muted), new LinearLayout.LayoutParams(0, -2, 1));
            captions.addView(ui.text(ui.t("More"), 9, ui.muted));
            addView(captions);
        } else note(ui.t("This model has no adjustable reasoning level."));
        note(ui.t("Applies to the next message. Context is preserved."));
        note(ui.t(!currentBackend.isEmpty() && !currentBackend.equals(selection.backend)
                ? "The next message creates a new chat with the selected agent and converted context."
                : "Only sending with another agent creates a new chat. The original chat stays unchanged."));
    }

    private void note(String value) {
        TextView note = ui.text(value, 10, ui.muted);
        ui.fixedLineHeight(note, 1.7f);
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(-1, -2);
        params.topMargin = ui.dp(10);
        addView(note, params);
    }
}
