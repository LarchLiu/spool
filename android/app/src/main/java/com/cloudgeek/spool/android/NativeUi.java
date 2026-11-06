package com.cloudgeek.spool.android;

import android.content.Context;
import android.content.res.ColorStateList;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.StateListDrawable;
import android.os.Build;
import android.text.InputType;
import android.text.TextUtils;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/** CSS-sized native primitives. Screens compose these instead of styling platform widgets. */
final class NativeUi extends WebPalette {
    final Context context;
    final boolean dark;
    final boolean chinese;
    final String locale;
    NativePopover activePopover;
    NativeAvatars avatars;
    java.util.concurrent.Executor codeWorker;
    NativeSyntax syntax;
    NativeCjkFallback cjkFonts;
    NativeToolDetails toolDetails;
    java.util.function.Function<String, org.json.JSONObject> people;

    NativeUi(Context context, boolean dark) {
        this(context, dark, false);
    }

    NativeUi(Context context, boolean dark, boolean chinese) {
        this(context, dark, chinese ? "zh-CN" : "en");
    }

    NativeUi(Context context, boolean dark, String locale) {
        super(dark);
        this.context = context;
        this.dark = dark;
        this.locale = locale;
        this.chinese = locale.startsWith("zh");
    }

    String label(String english, String simplifiedChinese) { return chinese ? simplifiedChinese : NativeText.translate(english, locale); }

    String t(String key, Object... values) { return NativeText.translate(key, locale, values); }

    void dismissMenus() {
        if (activePopover != null) activePopover.dismiss();
    }

    void buttonKeys(View view) {
        view.setOnKeyListener((target, code, event) -> {
            if (code != android.view.KeyEvent.KEYCODE_SPACE || !target.isEnabled()) return false;
            if (event.getAction() == android.view.KeyEvent.ACTION_DOWN && event.hasNoModifiers()) {
                if (event.getRepeatCount() == 0) target.setPressed(true);
                return true;
            }
            if (event.getAction() == android.view.KeyEvent.ACTION_UP) {
                boolean pressed = target.isPressed(); target.setPressed(false);
                if (pressed && !event.isCanceled() && event.hasNoModifiers()) target.performClick();
                return pressed;
            }
            return false;
        });
    }

    ImageView avatar(org.json.JSONObject person, int size) {
        return avatars == null ? NativeAvatars.placeholder(this, size) : avatars.image(this, person, size);
    }

    int dp(float value) {
        return Math.round(value * context.getResources().getDisplayMetrics().density);
    }

    LinearLayout column() {
        LinearLayout view = new LinearLayout(context);
        view.setOrientation(LinearLayout.VERTICAL);
        return view;
    }

    LinearLayout workspace() {
        LinearLayout view = column();
        view.setBackground(workspaceBackground());
        // CSS box shadows overflow their border box without reserving layout space.
        view.setClipChildren(false);
        return view;
    }

    LinearLayout row() {
        LinearLayout view = new LinearLayout(context);
        view.setGravity(Gravity.CENTER_VERTICAL);
        return view;
    }

    LinearLayout padded(int horizontal, int vertical) {
        LinearLayout view = column();
        view.setPadding(dp(horizontal), dp(vertical), dp(horizontal), dp(vertical));
        return view;
    }

    GradientDrawable shape(int color, float radius, int border) {
        GradientDrawable drawable = new GradientDrawable();
        drawable.setColor(color);
        drawable.setCornerRadius(dp(radius));
        if (border != 0) drawable.setStroke(dp(1), border);
        return drawable;
    }

    GradientDrawable background(int color, int radius, boolean border) {
        return shape(color, radius, border ? line : 0);
    }

    Drawable states(int color, float radius, int border, int pressed, int disabled) {
        StateListDrawable drawable = new StateListDrawable();
        drawable.addState(new int[] {-android.R.attr.state_enabled}, shape(disabled, radius, border));
        drawable.addState(new int[] {android.R.attr.state_pressed}, shape(pressed, radius, border));
        drawable.addState(new int[] {android.R.attr.state_hovered}, shape(controlHover, radius, border));
        drawable.addState(new int[] {android.R.attr.state_focused}, shape(pressed, radius, primary));
        drawable.addState(new int[0], shape(color, radius, border));
        return drawable;
    }

    int focusColor() {
        return android.graphics.Color.rgb(Math.round(android.graphics.Color.red(primary) * .74f + android.graphics.Color.red(surface) * .26f), Math.round(android.graphics.Color.green(primary) * .74f + android.graphics.Color.green(surface) * .26f), Math.round(android.graphics.Color.blue(primary) * .74f + android.graphics.Color.blue(surface) * .26f));
    }

    void focusRing(Canvas canvas, View child, float radius) {
        if (child == null || !child.isFocused() || !child.isEnabled()) return;
        Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG); paint.setStyle(Paint.Style.STROKE); paint.setStrokeWidth(dp(2));
        paint.setColor(focusColor());
        float offset = dp(4), rounding = dp(radius + 4);
        canvas.drawRoundRect(child.getLeft() - offset, child.getTop() - offset, child.getRight() + offset, child.getBottom() + offset, rounding, rounding, paint);
    }

    Drawable workspaceBackground() {
        return new NativeAtmosphere(this, false);
    }

    Drawable storyBackground() {
        return new NativeAtmosphere(this, true);
    }

    void typography(TextView view, int size, int color, int weight) {
        view.setTextSize(TypedValue.COMPLEX_UNIT_SP, size);
        view.setTextColor(color);
        view.setTextLocale(java.util.Locale.forLanguageTag(locale));
        view.setIncludeFontPadding(false);
        view.setLetterSpacing(0);
        view.setFontFeatureSettings("kern");
        view.setTypeface(NativeTypefaces.weight(weight));
        if (Build.VERSION.SDK_INT >= 28) view.setFallbackLineSpacing(view instanceof NativeTextView && ((NativeTextView) view).normalFallback);
        if (view instanceof NativeTextView) {
            NativeTextView text = (NativeTextView) view; text.fontWeight = weight;
            text.fallback = cjkFonts == null ? NativeCjkFallback.system() : cjkFonts;
            if (NativeCjkFallback.containsCjk(text.getText())) text.refreshFallback();
        }
    }

    void normalFallbackLineSpacing(TextView view) {
        if (view instanceof NativeTextView) ((NativeTextView) view).normalFallback = true;
        if (Build.VERSION.SDK_INT >= 28) view.setFallbackLineSpacing(true);
        else if (view instanceof NativeTextView) {
            NativeTextView text = (NativeTextView) view;
            text.fallback = cjkFonts == null ? NativeCjkFallback.system() : cjkFonts;
            text.refreshFallback();
        }
    }

    static int staticFontWeight(int weight) {
        if (weight < 400) return weight >= 300 ? 300 : 100;
        if (weight <= 500) return weight == 400 ? 400 : 500;
        return weight <= 700 ? 700 : 900;
    }

    void lineHeight(TextView view, float multiplier) {
        float natural = view.getPaint().getFontMetrics().descent - view.getPaint().getFontMetrics().ascent;
        view.setLineSpacing(view.getTextSize() * multiplier - natural, 1);
        if (view instanceof NativeTextView) {
            NativeTextView text = (NativeTextView) view;
            float density = context.getResources().getDisplayMetrics().density;
            // Blink stores both used font size and line-height in 1/64 CSS-pixel LayoutUnits.
            float usedSize = (float) Math.floor(view.getTextSize() / density * 64) / 64;
            text.lineBox = text.cssFontMetrics ? (float) Math.floor(usedSize * multiplier * 64) / 64 * density : view.getTextSize() * multiplier;
            if (text.fixedLineBox) text.applyLineBox();
        }
    }

    void fixedLineHeight(TextView view, float multiplier) {
        lineHeight(view, multiplier);
        if (view instanceof NativeTextView) {
            ((NativeTextView) view).fixedLineBox = true;
            ((NativeTextView) view).applyLineBox();
        }
        // CSS distributes extra leading above and below the glyphs, including the last line.
        view.setGravity((view.getGravity() & ~Gravity.VERTICAL_GRAVITY_MASK) | Gravity.CENTER_VERTICAL);
    }

    /** Explicit Markdown struts; editable inputs retain their existing native metric path. */
    void cssLineHeight(TextView view, float multiplier) {
        if (view instanceof NativeTextView) ((NativeTextView) view).cssFontMetrics = true;
        fixedLineHeight(view, multiplier);
    }

    /** CSS normal-height labels use rounded CSS font metrics, including their real fallback runs. */
    void cssNormalLabel(TextView view) {
        if (!(view instanceof NativeTextView)) return;
        NativeTextView text = (NativeTextView) view;
        float density = view.getResources().getDisplayMetrics().density;
        android.text.TextPaint base = new android.text.TextPaint(view.getPaint());
        base.setTextSize((float) Math.floor(base.getTextSize() / density * 64) / 64);
        android.graphics.Paint.FontMetrics primary = base.getFontMetrics();
        float ascent = Math.round(-primary.ascent), descent = Math.round(primary.descent);
        if (view.getText() instanceof android.text.Spanned) {
            android.text.Spanned value = (android.text.Spanned) view.getText();
            for (int start = 0; start < value.length();) {
                int end = value.nextSpanTransition(start, value.length(), android.text.style.MetricAffectingSpan.class);
                android.text.TextPaint run = new android.text.TextPaint(base);
                for (android.text.style.MetricAffectingSpan span : value.getSpans(start, end, android.text.style.MetricAffectingSpan.class)) span.updateMeasureState(run);
                android.graphics.Paint.FontMetrics metrics = run.getFontMetrics();
                ascent = Math.max(ascent, Math.round(-metrics.ascent)); descent = Math.max(descent, Math.round(metrics.descent)); start = end;
            }
        }
        text.cssNormalAscent = ascent; text.cssFontMetrics = true; text.fixedLineBox = true;
        text.lineBox = (ascent + descent) * density; text.applyLineBox();
    }

    void preciseText(TextView view) {
        preciseText(view, false);
    }

    void preciseText(TextView view, boolean ceilAdvance) {
        view.getPaint().setLinearText(true); view.getPaint().setSubpixelText(true);
        if (view instanceof NativeTextView) {
            ((NativeTextView) view).roundWidth = true;
            ((NativeTextView) view).ceilWidth = ceilAdvance;
        }
    }

    static float cssTextHeight(TextView view) {
        if (view instanceof NativeTextView && ((NativeTextView) view).fixedLineBox)
            return ((NativeTextView) view).lineBox * view.getLineCount() + view.getCompoundPaddingTop() + view.getCompoundPaddingBottom();
        return view.getMeasuredHeight();
    }

    static float cssTextOrigin(TextView owner) {
        return cssBlockOrigin(owner) + owner.getTotalPaddingTop();
    }

    static float cssBlockOrigin(View owner) {
        float y = 0;
        // Scrolling translates the already-painted document; it must not re-snap its text or underline.
        for (View view = owner; view.getParent() instanceof View; view = (View) view.getParent()) {
            // A virtual transcript row is a CSS origin boundary. Its own position depends on
            // placeholder heights above it; without this, one corrected row re-snaps every
            // mounted text view below and rebuilds their layouts during a fling.
            if (view instanceof NativeTranscriptViewport.Row) break;
            y += view.getTop();
        }
        return y;
    }

    static void cssTextRounding(View view, float y) {
        if (!(view instanceof NativeTextView) || !((NativeTextView) view).cssFontMetrics) return;
        NativeTextView text = (NativeTextView) view;
        text.cssPositioned = true; text.cssRoundingY = y; text.observeOrigin();
    }

    static void alignCssText(View view) {
        if (view instanceof NativeTextView) ((NativeTextView) view).alignOrigin();
    }

    TextView text(String value, int size, int color) {
        TextView view = new NativeTextView(context);
        typography(view, size, color, 400);
        lineHeight(view, 1.2f);
        view.setText(value);
        return view;
    }

    TextView bold(String value, int size) {
        TextView view = text(value, size, text);
        typography(view, size, text, 600);
        return view;
    }

    TextView eyebrow(String value, int size, float spacing) {
        TextView view = text(value, size, muted);
        typography(view, size, muted, 650);
        view.setLetterSpacing(spacing);
        return view;
    }

    static final class ActionButton extends Button {
        ActionButton(Context context) { super(context); }
        @Override public void setEnabled(boolean enabled) {
            super.setEnabled(enabled);
            setAlpha(enabled ? 1 : .45f);
        }
    }

    Button button(String title, boolean accent, Runnable click) {
        ActionButton view = new ActionButton(context);
        typography(view, 12, accent ? 0xffffffff : text, 400);
        view.setText(title);
        view.setAllCaps(false);
        view.setGravity(Gravity.CENTER);
        view.setMinHeight(0);
        view.setMinimumHeight(0);
        view.setMinWidth(0);
        view.setMinimumWidth(0);
        view.setPadding(dp(13), dp(9), dp(13), dp(9));
        view.setStateListAnimator(null);
        view.setBackground(states(accent ? primary : soft, 9, 0,
                accent ? primaryHover : controlActive, accent ? primary : soft));
        view.setOnClickListener(v -> click.run());
        return view;
    }

    Button quietButton(String title, Runnable click) {
        Button view = button(title, false, click);
        view.setTextColor(muted);
        view.setBackground(states(0x00000000, 9, 0, controlActive, 0x00000000));
        return view;
    }

    void setButtonIcon(Button button, String icon) {
        setButtonIcon(button, icon, 14, false);
    }

    void setButtonIcon(TextView view, String icon, int size, boolean end) {
        Drawable drawable = NativeIcons.drawable(context, icon, view.getCurrentTextColor(), dp(size));
        view.setCompoundDrawablesRelative(end ? null : drawable, null, end ? drawable : null, null);
        view.setCompoundDrawablePadding(dp(6));
    }

    EditText field(String hint, boolean password) {
        EditText view = new EditText(context);
        view.setSingleLine(true);
        view.setInputType(InputType.TYPE_CLASS_TEXT | (password
                ? InputType.TYPE_TEXT_VARIATION_PASSWORD : InputType.TYPE_TEXT_FLAG_CAP_SENTENCES));
        typography(view, 16, text, 400);
        view.setHintTextColor(muted);
        view.setHint(hint);
        view.setGravity(Gravity.CENTER_VERTICAL);
        view.setPadding(dp(13), dp(12), dp(13), dp(12));
        view.setBackground(states(input, 9, borderStrong, input, input));
        return view;
    }

    View divider() {
        View view = new View(context);
        view.setBackgroundColor(line);
        view.setLayoutParams(new LinearLayout.LayoutParams(-1, dp(1)));
        return view;
    }

    View space(int height) {
        View view = new View(context);
        view.setLayoutParams(new LinearLayout.LayoutParams(1, dp(height)));
        return view;
    }

    static final class IconButton extends ImageView {
        private final NativeUi ui;
        private String kind;
        private final int size;
        private final boolean send;
        private android.animation.ValueAnimator spin;
        private float glyphRotation;
        IconButton(NativeUi ui, String kind, int size, boolean send) {
            super(ui.context);
            this.ui = ui;
            this.size = size;
            this.send = send;
            setScaleType(ScaleType.CENTER);
            setKind(kind);
        }
        void setKind(String kind) {
            if (kind.equals(this.kind)) return;
            this.kind = kind;
            int glyphSize = send ? kind.equals("stop") ? 14 : kind.equals("spinner") ? 18 : 20 : size;
            setImageDrawable(NativeIcons.drawable(ui.context, kind, send ? 0xffffffff : ui.muted, ui.dp(glyphSize)));
            // CENTER uses intrinsic size; FIT_CENTER respects the requested CSS glyph size.
            setScaleType(ScaleType.FIT_CENTER);
            int pad = ui.dp((32 - glyphSize) / 2f);
            setPadding(pad, pad, pad, pad);
            if (send) setBackground(ui.states(ui.primary, 50, 0, ui.primaryHover, ui.borderStrong));
            updateSpin();
        }
        private void updateSpin() {
            if ("spinner".equals(kind) && isAttachedToWindow() && isShown() && getWindowVisibility() == VISIBLE
                    && android.animation.ValueAnimator.areAnimatorsEnabled()) {
                if (spin != null) return;
                spin = android.animation.ValueAnimator.ofFloat(0, 360);
                spin.setDuration(800); spin.setRepeatCount(android.animation.ValueAnimator.INFINITE);
                spin.setInterpolator(new android.view.animation.LinearInterpolator());
                spin.addUpdateListener(value -> { glyphRotation = (float) value.getAnimatedValue(); invalidate(); });
                spin.start();
            } else {
                if (spin != null) { spin.cancel(); spin = null; }
                glyphRotation = 0; invalidate();
            }
        }
        @Override protected void onDraw(Canvas canvas) {
            int save = canvas.save(); canvas.rotate(glyphRotation, getWidth() / 2f, getHeight() / 2f);
            super.onDraw(canvas); canvas.restoreToCount(save);
        }
        @Override protected void onAttachedToWindow() { super.onAttachedToWindow(); updateSpin(); }
        @Override protected void onDetachedFromWindow() { if (spin != null) { spin.cancel(); spin = null; } glyphRotation = 0; super.onDetachedFromWindow(); }
        @Override protected void onVisibilityChanged(View changed, int visibility) { super.onVisibilityChanged(changed, visibility); updateSpin(); }
        @Override protected void onWindowVisibilityChanged(int visibility) { super.onWindowVisibilityChanged(visibility); updateSpin(); }
        @Override public void setEnabled(boolean enabled) {
            super.setEnabled(enabled);
            setAlpha(enabled || send ? 1 : .45f);
        }
    }

    IconButton icon(String kind, String label, Runnable click) {
        return icon(kind, 18, label, click);
    }

    IconButton icon(String kind, int size, String label, Runnable click) {
        IconButton view = new IconButton(this, kind, size, false);
        view.setContentDescription(label);
        view.setFocusable(true);
        view.setBackground(states(0x00000000, 50, 0, controlActive, 0x00000000));
        view.setOnClickListener(v -> click.run());
        view.setLayoutParams(new LinearLayout.LayoutParams(dp(32), dp(32)));
        return view;
    }

    IconButton sendButton(String label, Runnable click) {
        IconButton view = new IconButton(this, "send", 20, true);
        view.setContentDescription(label);
        view.setFocusable(true);
        view.setOnClickListener(v -> click.run());
        view.setLayoutParams(new LinearLayout.LayoutParams(dp(32), dp(32)));
        return view;
    }

    ImageView decorativeIcon(String kind, int size) {
        ImageView view = new ImageView(context);
        view.setImageDrawable(NativeIcons.drawable(context, kind, muted, dp(size)));
        view.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
        view.setLayoutParams(new LinearLayout.LayoutParams(dp(size), dp(size)));
        return view;
    }

    ImageView logo(int size) {
        ImageView view = new ImageView(context);
        view.setImageResource(R.drawable.spool_logo);
        view.setContentDescription("Spool");
        view.setLayoutParams(new LinearLayout.LayoutParams(dp(size), dp(size)));
        return view;
    }

    LinearLayout toolbar(String title, Runnable back) {
        LinearLayout view = row();
        view.setPadding(dp(16), 0, dp(16), 0);
        view.setMinimumHeight(dp(50));
        view.addView(icon("back", 18, label("Back", "返回"), back));
        TextView label = bold(title, 12);
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(0, -2, 1);
        params.setMarginStart(dp(10));
        view.addView(label, params);
        return view;
    }

    final class Header extends LinearLayout {
        final TextView title, subtitle;
        final LinearLayout online;
        private Object peopleSignature;
        Header(String titleText, String subtitleText, Runnable menu, Runnable people, Runnable files) {
            super(context);
            setGravity(Gravity.CENTER_VERTICAL);
            setPadding(dp(16), 0, dp(16), 0);
            setBackgroundColor(header);
            addView(icon("menu", 18, label("Open sidebar", "打开侧边栏"), menu));
            LinearLayout titles = column();
            title = bold(titleText, 12);
            subtitle = text(subtitleText, 12, muted);
            title.setSingleLine();
            title.setEllipsize(TextUtils.TruncateAt.END);
            subtitle.setSingleLine();
            subtitle.setEllipsize(TextUtils.TruncateAt.END);
            subtitle.setLetterSpacing(.02f);
            preciseText(title); preciseText(subtitle);
            title.setMaxWidth(dp(getResources().getConfiguration().screenWidthDp * .45f));
            titles.addView(title, new LinearLayout.LayoutParams(-2, -2));
            LinearLayout.LayoutParams sub = new LinearLayout.LayoutParams(-2, -2);
            sub.topMargin = dp(4);
            titles.addView(subtitle, sub);
            LinearLayout.LayoutParams copy = new LinearLayout.LayoutParams(0, -2, 1);
            copy.setMarginStart(dp(10));
            copy.setMarginEnd(dp(8));
            addView(titles, copy);
            online = row();
            online.setContentDescription(label("People chat", "成员聊天"));
            online.setFocusable(true);
            online.setBackground(states(0x00000000, 9, 0, controlActive, 0x00000000));
            online.setOnClickListener(v -> people.run());
            bindPeople(java.util.Collections.emptyList());
            addView(online, new LinearLayout.LayoutParams(-2, dp(32)));
            LinearLayout.LayoutParams file = new LinearLayout.LayoutParams(dp(32), dp(32));
            file.setMarginStart(dp(8));
            addView(icon("files", 18, label("Open file sidebar", "打开文件侧边栏"), files), file);
            // The separate divider completes the CSS 50px border box, also at fractional DPR.
            setLayoutParams(new LinearLayout.LayoutParams(-1, dp(50) - dp(1)));
        }

        void bindPeople(java.util.List<org.json.JSONObject> people) {
            bindPeople(people, 0);
        }

        void bindPeople(java.util.List<org.json.JSONObject> people, int unread) {
            org.json.JSONArray identity = new org.json.JSONArray().put(unread);
            for (org.json.JSONObject person : people) identity.put(person);
            if (NativeChatState.equivalent(peopleSignature, identity)) return;
            peopleSignature = NativeChatState.snapshot(identity);
            online.removeAllViews();
            if (people.isEmpty()) online.addView(decorativeIcon("people", 14));
            else online.addView(new NativeAvatarStack(NativeUi.this, people, false));
            if (unread > 0) {
                TextView badge = text(NativePeopleModel.badge(unread), 10, 0xffffffff);
                typography(badge, 10, 0xffffffff, 600); badge.setGravity(Gravity.CENTER); badge.setMinWidth(dp(18)); badge.setPadding(dp(5), 0, dp(5), 0); badge.setBackground(shape(primary, 10, 0));
                LinearLayout.LayoutParams position = new LinearLayout.LayoutParams(-2, dp(18)); position.setMarginStart(dp(8)); online.addView(badge, position);
            }
            online.setContentDescription(t("{count} online", "count", people.size()) + (unread > 0 ? ", " + t("{count} unread messages", "count", unread) : ""));
        }

        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            int maximum = Math.round(MeasureSpec.getSize(widthSpec) * .45f);
            if (title.getMaxWidth() != maximum) title.setMaxWidth(maximum);
            super.onMeasure(widthSpec, heightSpec);
        }
    }

    final class ChoiceButton extends LinearLayout {
        final ImageView symbol, chevron;
        final TextView label;
        final boolean permission;
        boolean danger;
        String kind;
        int maximumLabel = 100;
        int viewportWidth;
        private int fontSize;
        private int inkColor;
        private String inkKind;
        private boolean inkReady;
        ChoiceButton(boolean permission, Runnable click) {
            super(context);
            this.permission = permission;
            setGravity(Gravity.CENTER_VERTICAL);
            setPadding(dp(10), 0, dp(10), 0);
            symbol = decorativeIcon(permission ? "shield-question" : "codex", permission ? 15 : 14);
            addView(symbol);
            label = text("", permission ? 10 : 9, muted);
            label.setSingleLine();
            label.setEllipsize(TextUtils.TruncateAt.END);
            LinearLayout.LayoutParams name = new LinearLayout.LayoutParams(-2, -2);
            name.setMarginStart(dp(permission ? 6 : 5));
            name.setMarginEnd(dp(permission ? 6 : 5));
            addView(label, name);
            chevron = decorativeIcon("chevron-down", permission ? 11 : 12);
            LinearLayout.LayoutParams chevronParams = new LinearLayout.LayoutParams(dp(permission ? 11 : 12), dp(permission ? 11 : 12));
            chevronParams.setMarginStart(dp(permission ? 6 : 0));
            // Keep the icon gap when the permission label is hidden by the mobile breakpoint.
            name.setMarginEnd(dp(permission ? 0 : 5));
            addView(chevron, chevronParams);
            preciseText(label, true);
            setFocusable(true);
            StateListDrawable background = new StateListDrawable();
            background.addState(new int[] {-android.R.attr.state_enabled}, shape(permission ? 0 : soft, 99, 0));
            background.addState(new int[] {android.R.attr.state_activated}, shape(controlActive, 99, 0));
            background.addState(new int[] {android.R.attr.state_pressed}, shape(controlActive, 99, 0));
            background.addState(new int[] {android.R.attr.state_hovered}, shape(controlHover, 99, 0));
            background.addState(new int[] {android.R.attr.state_focused}, shape(permission ? 0 : soft, 99, primary));
            background.addState(new int[0], shape(permission ? 0 : soft, 99, 0));
            setBackground(background);
            setOnClickListener(v -> click.run());
        }
        void bind(String text, String kind, String description) {
            if (!TextUtils.equals(label.getText(), text)) label.setText(text);
            this.kind = kind;
            updateInk();
            setContentDescription(description);
        }
        void setDanger(boolean danger) { if (this.danger != danger) { this.danger = danger; updateInk(); } }
        void setViewportWidth(int width) {
            if (viewportWidth == width) return;
            viewportWidth = width;
            forceLayout();
        }
        private void updateInk() {
            if (label == null || symbol == null || chevron == null) return;
            int color = danger ? primary : isEnabled() && (isPressed() || isHovered() || !permission && isActivated()) ? NativeUi.this.text : muted;
            if (inkReady && inkColor == color && java.util.Objects.equals(inkKind, kind)) return;
            inkReady = true; inkColor = color; inkKind = kind;
            label.setTextColor(color);
            if (kind != null) symbol.setImageDrawable(NativeIcons.drawable(context, kind, color, dp(permission ? 15 : 14)));
            chevron.setImageTintList(ColorStateList.valueOf(color));
        }
        @Override protected void drawableStateChanged() { super.drawableStateChanged(); updateInk(); }
        @Override public void setEnabled(boolean enabled) {
            super.setEnabled(enabled);
            setAlpha(enabled ? 1 : .45f);
        }
        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            boolean compact = permission && (viewportWidth > 0 ? viewportWidth <= dp(400) : getResources().getConfiguration().screenWidthDp <= 400);
            boolean phone = viewportWidth > 0 ? viewportWidth <= dp(600) : getResources().getConfiguration().screenWidthDp <= 600;
            int size = permission ? phone ? 10 : 11 : phone ? 9 : 10;
            if (fontSize != size) { fontSize = size; label.setTextSize(TypedValue.COMPLEX_UNIT_SP, size); lineHeight(label, 1.2f); }
            label.setVisibility(compact ? GONE : VISIBLE);
            int labelWidth = dp(permission ? phone ? 77 : 132 : maximumLabel);
            if (MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED) {
                LinearLayout.LayoutParams name = (LinearLayout.LayoutParams) label.getLayoutParams();
                LinearLayout.LayoutParams arrow = (LinearLayout.LayoutParams) chevron.getLayoutParams();
                int fixed = getPaddingLeft() + getPaddingRight() + dp(permission ? 15 : 14) + arrow.width
                        + name.getMarginStart() + name.getMarginEnd() + arrow.getMarginStart() + arrow.getMarginEnd();
                labelWidth = Math.min(labelWidth, Math.max(0, MeasureSpec.getSize(widthSpec) - fixed));
            }
            if (label.getMaxWidth() != labelWidth) label.setMaxWidth(labelWidth);
            super.onMeasure(widthSpec, heightSpec);
        }
    }

    final class Composer extends LinearLayout {
        final EditText input;
        final NativeAttachmentCards attachments;
        final android.widget.HorizontalScrollView attachmentScroll;
        final ChoiceButton model, permission;
        final IconButton send, add;
        final NativeSurface box;
        final NativeComposerControls bottom;
        final NativeComposerCommands commands;
        private Boolean phoneLayout;
        Composer(String hint, Runnable addFiles, Runnable chooseModel, Runnable choosePermission, Runnable submit) {
            super(context);
            setOrientation(VERTICAL);
            setPadding(dp(12), 0, dp(12), dp(10));
            setClipChildren(false);
            setClipToPadding(false);
            box = new NativeComposerBox(NativeUi.this);
            attachmentScroll = new android.widget.HorizontalScrollView(context);
            attachmentScroll.setHorizontalScrollBarEnabled(false);
            attachmentScroll.setPadding(dp(12), dp(12), dp(12), 0);
            attachments = new NativeAttachmentCards(NativeUi.this, false);
            attachmentScroll.addView(attachments);
            attachmentScroll.setVisibility(GONE);
            box.addView(attachmentScroll, new LinearLayout.LayoutParams(-1, -2));
            input = new NativeAutosizeInput(NativeUi.this, hint, 16, 1.6f, 6);
            input.setPadding(dp(15), dp(14), dp(15), dp(8));
            box.addView(input, new LinearLayout.LayoutParams(-1, -2));
            add = icon("plus", 20, label("Add files or images", "添加文件或图片"), addFiles);
            permission = new ChoiceButton(true, choosePermission);
            model = new ChoiceButton(false, chooseModel);
            send = sendButton(label("Send message", "发送消息"), submit);
            bottom = new NativeComposerControls(NativeUi.this, add, permission, model, send);
            box.addView(bottom, new LinearLayout.LayoutParams(-1, dp(49)));
            addView(box, new LinearLayout.LayoutParams(-1, -2));
            commands = new NativeComposerCommands(NativeUi.this, this, submit);
        }

        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            int width = MeasureSpec.getSize(widthSpec);
            boolean phone = width <= dp(600);
            int horizontal = phone ? dp(12) : Math.max(Math.round(width * .04f), dp(26));
            setPadding(horizontal, 0, horizontal, dp(phone ? 10 : 12));
            if (phoneLayout == null || phoneLayout != phone) {
                phoneLayout = phone;
                input.setTextSize(TypedValue.COMPLEX_UNIT_SP, phone ? 16 : 14);
                lineHeight(input, 1.6f);
                input.setPadding(dp(phone ? 15 : 19), dp(phone ? 14 : 17), dp(phone ? 15 : 19), dp(8));
            }
            LinearLayout.LayoutParams boxParams = (LinearLayout.LayoutParams) box.getLayoutParams();
            boxParams.width = Math.min(dp(900), Math.max(0, width - horizontal * 2));
            boxParams.gravity = Gravity.CENTER_HORIZONTAL;
            for (int i = 0; i < getChildCount(); i++) {
                LinearLayout.LayoutParams params = (LinearLayout.LayoutParams) getChildAt(i).getLayoutParams();
                params.width = boxParams.width; params.gravity = Gravity.CENTER_HORIZONTAL;
            }
            bottom.setViewportWidth(width);
            super.onMeasure(widthSpec, heightSpec);
        }

        @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
            super.onLayout(changed, left, top, right, bottom);
            // Snap paint in workspace CSS coordinates, not each View's integer pixel bounds.
            box.setCssBorderBox(box.getWidth(), box.getHeight(), getLeft() + box.getLeft(), getTop() + box.getTop());
        }
    }

    NativeWelcome welcome(String titleText, String description, String project, String note) {
        return new NativeWelcome(this, titleText, description, project, note);
    }

    TextView iconLabel(String title, String kind, int size, int iconSize) {
        TextView view = text(title, size, subtle);
        setButtonIcon(view, kind, iconSize, false);
        return view;
    }

    LinearLayout message(CharSequence content, boolean mine) {
        LinearLayout view = column();
        TextView copy = text("", 14, text);
        copy.setText(content);
        lineHeight(copy, mine ? 1.75f : 1.85f);
        copy.setTextIsSelectable(true);
        copy.setMovementMethod(android.text.method.LinkMovementMethod.getInstance());
        if (mine) {
            copy.setPadding(dp(17), dp(13), dp(17), dp(13));
            GradientDrawable bubble = shape(soft, 19, 0);
            bubble.setCornerRadii(new float[] {dp(19),dp(19),dp(19),dp(19),dp(5),dp(5),dp(19),dp(19)});
            copy.setBackground(bubble);
        }
        view.addView(copy, new LinearLayout.LayoutParams(-1, -2));
        return view;
    }

    NativeThemeOption themeOption(String label, String icon, boolean selected, Runnable click) {
        return new NativeThemeOption(this, label, icon, selected, click);
    }
}
