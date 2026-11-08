package com.cloudgeek.spool.android;

import android.content.Context;
import android.util.AttributeSet;
import android.widget.TextView;

/** Full-width Markdown whose text is laid out once RecyclerView measures its new document. */
public final class NativeMarkwonTextView extends TextView {
    public NativeMarkwonTextView(Context context, AttributeSet attributes) { super(context, attributes); }

    @Override protected void onMeasure(int widthSpec, int heightSpec) {
        // WRAP_CONTENT in the layout keeps TextView.setText from synchronously shaping
        // the empty recycled text or a new document at the previous holder's width.
        // Markdown still occupies the entire available row, as MATCH_PARENT did.
        if (MeasureSpec.getMode(widthSpec) == MeasureSpec.AT_MOST)
            widthSpec = MeasureSpec.makeMeasureSpec(MeasureSpec.getSize(widthSpec), MeasureSpec.EXACTLY);
        super.onMeasure(widthSpec, heightSpec);
    }
}
