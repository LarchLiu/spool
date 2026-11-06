package com.cloudgeek.spool.android;

import android.text.InputType;
import android.view.View;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.LinearLayout;
import org.json.JSONArray;
import org.json.JSONObject;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Supplier;

/** Primitive MCP elicitation inputs. The server validates submitted values. */
final class NativeApprovalForm extends LinearLayout {
    private final Map<String, Supplier<Object>> values = new LinkedHashMap<>();
    private final List<View> controls = new ArrayList<>();

    NativeApprovalForm(NativeUi ui, JSONArray fields) {
        super(ui.context);
        setOrientation(VERTICAL);
        for (int i = 0; i < fields.length(); i++) {
            JSONObject field = fields.optJSONObject(i);
            if (field == null) continue;
            String name = field.optString("name"), type = field.optString("type"), label = field.optString("label", name);
            addView(ui.text(label + (field.optBoolean("required") && !type.equals("boolean") ? " *" : ""), 12, ui.text));
            if (!field.optString("description").isEmpty()) addView(ui.text(field.optString("description"), 11, ui.muted));
            JSONArray options = field.optJSONArray("options");
            if (type.equals("boolean")) {
                CheckBox input = new CheckBox(ui.context);
                input.setChecked(field.optBoolean("default"));
                input.setContentDescription(label);
                controls.add(input); addView(input);
                values.put(name, input::isChecked);
            } else if (type.equals("array") && options != null) {
                List<CheckBox> inputs = new ArrayList<>();
                JSONArray defaults = field.optJSONArray("default");
                for (int j = 0; j < options.length(); j++) {
                    JSONObject option = options.optJSONObject(j);
                    if (option == null) continue;
                    String value = option.optString("value");
                    CheckBox input = new CheckBox(ui.context);
                    input.setTag(value); input.setText(option.optString("label", value)); input.setTextColor(ui.text);
                    if (defaults != null) for (int k = 0; k < defaults.length(); k++) if (value.equals(defaults.optString(k))) input.setChecked(true);
                    inputs.add(input); controls.add(input); addView(input);
                }
                values.put(name, () -> {
                    JSONArray result = new JSONArray();
                    for (CheckBox input : inputs) if (input.isChecked()) result.put(input.getTag());
                    return result;
                });
            } else if (options != null && options.length() > 0) {
                String[] ids = new String[options.length() + 1], labels = new String[ids.length];
                ids[0] = ""; labels[0] = "—";
                for (int j = 0; j < options.length(); j++) {
                    JSONObject option = options.optJSONObject(j);
                    ids[j + 1] = option.optString("value"); labels[j + 1] = option.optString("label", ids[j + 1]);
                }
                String[] selected = {field.isNull("default") ? "" : field.optString("default")};
                NativeSelect[] select = new NativeSelect[1];
                select[0] = new NativeSelect(ui, label, selected[0], ids, labels, false, value -> {
                    selected[0] = value; select[0].bind(value);
                });
                controls.add(select[0]); addView(select[0]);
                values.put(name, () -> selected[0].isEmpty() ? null : selected[0]);
            } else {
                EditText input = ui.field(label, false);
                input.setContentDescription(label);
                if (!field.isNull("default")) input.setText(field.optString("default"));
                boolean numeric = type.equals("integer") || type.equals("number");
                if (numeric) input.setInputType(InputType.TYPE_CLASS_NUMBER | InputType.TYPE_NUMBER_FLAG_SIGNED
                        | (type.equals("number") ? InputType.TYPE_NUMBER_FLAG_DECIMAL : 0));
                controls.add(input); addView(input);
                values.put(name, () -> {
                    String value = input.getText().toString();
                    if (value.isEmpty() && !field.optBoolean("required")) return null;
                    if (!numeric) return value;
                    try { return Double.valueOf(value); }
                    catch (NumberFormatException invalid) { input.setError(label); throw invalid; }
                });
            }
            addView(ui.space(10));
        }
    }

    JSONObject content() {
        JSONObject result = new JSONObject();
        for (Map.Entry<String, Supplier<Object>> entry : values.entrySet()) {
            Object value = entry.getValue().get();
            if (value != null) try { result.put(entry.getKey(), value); }
            catch (org.json.JSONException invalid) { throw new IllegalArgumentException(invalid); }
        }
        return result;
    }

    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled);
        for (View control : controls) control.setEnabled(enabled);
    }
}
