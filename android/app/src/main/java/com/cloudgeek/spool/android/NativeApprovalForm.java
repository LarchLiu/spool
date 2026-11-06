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
    private EditText jsonForm, feedback;

    NativeApprovalForm(NativeUi ui, JSONObject request) {
        this(ui, request.optJSONArray("fields") == null ? new JSONArray() : request.optJSONArray("fields"));
        if (request.optBoolean("json_form")) {
            addView(ui.text(ui.t("Form values (JSON)"), 12, ui.text));
            addSchemaPreview(ui, request.optJSONObject("schema"));
            jsonForm = multiline(ui, ui.t("Form values (JSON)"));
            jsonForm.setContentDescription(ui.t("Form values (JSON)"));
            jsonForm.setText(schemaDefaults(request.optJSONObject("schema"), 0).toString());
            jsonForm.setMinLines(5); controls.add(jsonForm); addView(jsonForm);
        }
        if (request.optBoolean("feedback")) {
            addView(ui.text(ui.t("Feedback for denial (optional)"), 12, ui.text));
            feedback = multiline(ui, ui.t("Feedback for denial (optional)"));
            feedback.setFilters(new android.text.InputFilter[] {new android.text.InputFilter.LengthFilter(4096)});
            controls.add(feedback); addView(feedback);
        }
    }

    private void addSchemaPreview(NativeUi ui, JSONObject schema) {
        android.widget.TextView value = ui.text(schema == null ? "{}" : schema.toString(), 11, ui.muted);
        value.setTypeface(android.graphics.Typeface.MONOSPACE); value.setTextIsSelectable(true);
        value.setVisibility(GONE);
        android.widget.Button toggle = ui.quietButton(ui.t("Requested form schema"), () -> value.setVisibility(value.getVisibility() == GONE ? VISIBLE : GONE));
        controls.add(toggle); addView(toggle); addView(value);
    }

    private static EditText multiline(NativeUi ui, String label) {
        EditText input = ui.field(label, false);
        input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE | InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
        input.setSingleLine(false); input.setGravity(android.view.Gravity.TOP | android.view.Gravity.START);
        return input;
    }

    private static Object schemaDefaults(JSONObject schema, int depth) {
        if (schema == null || depth > 12) return JSONObject.NULL;
        if (schema.has("default")) return schema.opt("default");
        if (schema.has("const")) return schema.opt("const");
        JSONArray options = schema.optJSONArray("enum");
        if (options != null && options.length() > 0) return options.opt(0);
        JSONObject properties = schema.optJSONObject("properties");
        if (properties != null || schema.optString("type").equals("object")) {
            JSONObject result = new JSONObject();
            if (properties != null) { java.util.Iterator<String> names = properties.keys(); while (names.hasNext()) { String name = names.next(); try { result.put(name, schemaDefaults(properties.optJSONObject(name), depth + 1)); } catch (org.json.JSONException invalid) { throw new IllegalArgumentException(invalid); } } }
            return result;
        }
        switch (schema.optString("type")) {
            case "array": return new JSONArray();
            case "boolean": return false;
            case "integer": case "number": return schema.optDouble("minimum", 0);
            default: return "";
        }
    }

    NativeApprovalForm(NativeUi ui, JSONArray fields) {
        super(ui.context);
        setOrientation(VERTICAL);
        for (int i = 0; i < fields.length(); i++) {
            JSONObject field = fields.optJSONObject(i);
            if (field == null) continue;
            String name = field.optString("name"), type = field.optString("type"), label = field.optString("label", name);
            addView(ui.text(ui.t(label) + (field.optBoolean("required") && !type.equals("boolean") ? " *" : ""), 12, ui.text));
            if (!field.optString("description").isEmpty()) addView(ui.text(ui.t(field.optString("description")), 11, ui.muted));
            JSONArray options = field.optJSONArray("options");
            if (type.equals("boolean")) {
                CheckBox input = new CheckBox(ui.context);
                input.setChecked(field.optBoolean("default"));
                input.setContentDescription(label);
                controls.add(input); addView(input);
                values.put(name, input::isChecked);
            } else if (type.equals("array")) {
                if (options == null) options = new JSONArray();
                List<CheckBox> inputs = new ArrayList<>();
                JSONArray defaults = field.optJSONArray("default");
                for (int j = 0; j < options.length(); j++) {
                    JSONObject option = options.optJSONObject(j);
                    if (option == null) continue;
                    String value = option.optString("value");
                    CheckBox input = new CheckBox(ui.context);
                    input.setTag(value); input.setText(ui.t(option.optString("label", value))); input.setTextColor(ui.text);
                    if (defaults != null) for (int k = 0; k < defaults.length(); k++) if (value.equals(defaults.optString(k))) input.setChecked(true);
                    inputs.add(input); controls.add(input); addView(input);
                    if (!option.optString("description").isEmpty()) addView(ui.text(option.optString("description"), 11, ui.muted));
                }
                EditText other = field.optBoolean("allow_other") ? ui.field(ui.t("Other answer"), false) : null;
                if (other != null) { controls.add(other); addView(other); }
                values.put(name, () -> {
                    JSONArray result = new JSONArray();
                    for (CheckBox input : inputs) if (input.isChecked()) result.put(input.getTag());
                    if (other != null && other.length() > 0) result.put(other.getText().toString());
                    return result;
                });
            } else if (options != null && options.length() > 0) {
                String[] ids = new String[options.length() + 1], labels = new String[ids.length], descriptions = new String[ids.length];
                ids[0] = ""; labels[0] = "—";
                for (int j = 0; j < options.length(); j++) {
                    JSONObject option = options.optJSONObject(j);
                    ids[j + 1] = option.optString("value"); labels[j + 1] = ui.t(option.optString("label", ids[j + 1])); descriptions[j+1] = option.optString("description");
                }
                String[] selected = {field.isNull("default") ? "" : field.optString("default")};
                EditText other = field.optBoolean("allow_other") ? ui.field(ui.t("Other answer"), false) : null;
                NativeSelect[] select = new NativeSelect[1];
                select[0] = new NativeSelect(ui, label, selected[0], ids, labels, descriptions, false, value -> {
                    selected[0] = value; select[0].bind(value);
                    if (other != null) other.setText("");
                });
                controls.add(select[0]); addView(select[0]);
                if (other != null) { controls.add(other); addView(other); other.setContentDescription(label + ": " + ui.t("Other answer")); }
                values.put(name, () -> other != null && other.length() > 0 ? other.getText().toString() : selected[0].isEmpty() ? null : selected[0]);
            } else {
                EditText input = field.optBoolean("multiline") || type.equals("json") ? multiline(ui, label) : ui.field(label, false);
                input.setContentDescription(label);
                if (!field.isNull("default")) input.setText(field.optString("default"));
                boolean numeric = type.equals("integer") || type.equals("number");
                if (type.equals("json")) { input.setTypeface(android.graphics.Typeface.MONOSPACE); input.setMinLines(3); }
                if (field.optBoolean("secret")) input.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
                if (numeric) input.setInputType(InputType.TYPE_CLASS_NUMBER | InputType.TYPE_NUMBER_FLAG_SIGNED
                        | (type.equals("number") ? InputType.TYPE_NUMBER_FLAG_DECIMAL : 0));
                controls.add(input); addView(input);
                values.put(name, () -> {
                    String value = input.getText().toString();
                    if (value.isEmpty() && !field.optBoolean("required")) return null;
                    if (type.equals("json")) try { return new JSONObject(value); } catch (org.json.JSONException invalid) { input.setError(label); throw new IllegalArgumentException(invalid); }
                    if (!numeric) return value;
                    try { return Double.valueOf(value); }
                    catch (NumberFormatException invalid) { input.setError(label); throw invalid; }
                });
            }
            addView(ui.space(10));
        }
    }

    JSONObject content() {
        if (jsonForm != null) try { return new JSONObject(jsonForm.getText().toString()); }
        catch (org.json.JSONException invalid) { jsonForm.setError("JSON"); throw new IllegalArgumentException(invalid); }
        JSONObject result = new JSONObject();
        for (Map.Entry<String, Supplier<Object>> entry : values.entrySet()) {
            Object value = entry.getValue().get();
            if (value != null) try { result.put(entry.getKey(), value); }
            catch (org.json.JSONException invalid) { throw new IllegalArgumentException(invalid); }
        }
        return result;
    }

    String feedback() { return feedback == null ? "" : feedback.getText().toString(); }

    @Override public void setEnabled(boolean enabled) {
        super.setEnabled(enabled);
        for (View control : controls) control.setEnabled(enabled);
    }
}
