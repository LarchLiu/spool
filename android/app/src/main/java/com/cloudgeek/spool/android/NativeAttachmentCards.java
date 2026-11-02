package com.cloudgeek.spool.android;

import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.function.Consumer;

/** Web attachment flex layout: horizontal drafts; right-aligned, wrapping transcript rows. */
final class NativeAttachmentCards extends ViewGroup {
    static final class Item {
        final String id, name, mediaType, excerpt, imageKey;
        final Runnable preview;
        final Consumer<ImageView> load;
        Item(String id, String name, String mediaType, String excerpt, String imageKey, Runnable preview, Consumer<ImageView> load) {
            this.id = id; this.name = name; this.mediaType = mediaType; this.excerpt = excerpt;
            this.imageKey = imageKey; this.preview = preview; this.load = load;
        }
    }
    private static final class Bound {
        NativeAttachmentCard card;
        String imageKey;
    }
    private final NativeUi ui;
    private final boolean wrap;
    private Map<String, Bound> bindings = new HashMap<>();
    float maximumFraction = 1;
    NativeAttachmentCards(NativeUi ui, boolean wrap) {
        super(ui.context);
        this.ui = ui;
        this.wrap = wrap;
        setPadding(ui.dp(2), ui.dp(2), ui.dp(2), ui.dp(2));
        setContentDescription(ui.t("Attachments"));
    }

    void bind(List<Item> files) {
        List<View> wanted = new ArrayList<>(); Map<String, Bound> next = new HashMap<>(); Map<String, Integer> occurrences = new HashMap<>();
        for (Item file : files) {
            String key = new org.json.JSONArray().put(file.id).put(occurrences.merge(file.id, 1, Integer::sum)).toString();
            Bound binding = bindings.get(key);
            if (binding == null) {
                binding = new Bound();
                binding.card = new NativeAttachmentCard(ui, file.name, file.mediaType, file.excerpt, null, file.preview, null);
            } else binding.card.bind(file.name, file.mediaType, file.excerpt, file.preview);
            NativeAttachmentCard card = binding.card;
            if (card.image == null) binding.imageKey = null;
            else if (!file.imageKey.equals(binding.imageKey)) {
                binding.imageKey = file.imageKey;
                card.image.setTag(null); card.image.setImageDrawable(null);
                if (file.load != null) file.load.accept(card.image);
            }
            wanted.add(card); next.put(key, binding);
        }
        for (Map.Entry<String, Bound> previous : bindings.entrySet()) if (!next.containsKey(previous.getKey())) previous.getValue().card.retire();
        NativeKeyedViews.sync(this, wanted); bindings = next;
    }

    void retire() { for (Bound binding : bindings.values()) binding.card.retire(); }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        int maximum = MeasureSpec.getMode(widthSpec) == MeasureSpec.UNSPECIFIED ? Integer.MAX_VALUE : MeasureSpec.getSize(widthSpec);
        if (maximum != Integer.MAX_VALUE && wrap) maximum = Math.round(maximum * maximumFraction);
        int available = Math.max(0, maximum - getPaddingLeft() - getPaddingRight());
        int rowWidth = 0, rowHeight = 0, height = 0, width = 0;
        for (int i = 0; i < getChildCount(); i++) {
            View child = getChildAt(i);
            if (child.getVisibility() == GONE) continue;
            child.measure(MeasureSpec.makeMeasureSpec(child.getLayoutParams().width, MeasureSpec.EXACTLY),
                    MeasureSpec.makeMeasureSpec(child.getLayoutParams().height, MeasureSpec.EXACTLY));
            if (wrap && rowWidth > 0 && rowWidth + ui.dp(9) + child.getMeasuredWidth() > available) {
                width = Math.max(width, rowWidth);
                height += rowHeight + ui.dp(9);
                rowWidth = rowHeight = 0;
            }
            if (rowWidth > 0) rowWidth += ui.dp(9);
            rowWidth += child.getMeasuredWidth();
            rowHeight = Math.max(rowHeight, child.getMeasuredHeight());
        }
        width = Math.max(width, rowWidth) + getPaddingLeft() + getPaddingRight();
        height += rowHeight + getPaddingTop() + getPaddingBottom();
        setMeasuredDimension(wrap ? Math.min(maximum, width) : resolveSize(width, widthSpec), resolveSize(height, heightSpec));
    }

    @Override protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        int from = 0, y = getPaddingTop(), available = getWidth() - getPaddingLeft() - getPaddingRight();
        while (from < getChildCount()) {
            int end = from, width = 0, height = 0;
            while (end < getChildCount()) {
                View child = getChildAt(end);
                if (child.getVisibility() == GONE) { end++; continue; }
                int next = width + (width > 0 ? ui.dp(9) : 0) + child.getMeasuredWidth();
                if (wrap && width > 0 && next > available) break;
                width = next;
                height = Math.max(height, child.getMeasuredHeight());
                end++;
            }
            int x = wrap ? getWidth() - getPaddingRight() - width : getPaddingLeft();
            for (int i = from; i < end; i++) {
                View child = getChildAt(i);
                if (child.getVisibility() == GONE) continue;
                child.layout(x, y, x + child.getMeasuredWidth(), y + child.getMeasuredHeight());
                x += child.getMeasuredWidth() + ui.dp(9);
            }
            y += height + ui.dp(9);
            from = end;
        }
    }
}
