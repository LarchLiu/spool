package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.app.Activity;
import android.graphics.Paint;
import android.widget.TextView;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.text.Spannable;
import android.text.Selection;
import android.text.TextPaint;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.nio.file.Files;
import java.nio.file.Path;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;

/** Fractional table-font advances, independent of table width allocation and line-height. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeTableWrapTest {
    private JSONObject reference() throws Exception {
        try(java.io.InputStream input=getClass().getResourceAsStream("/web-table-wrap.json")) {
            assertNotNull("Run table-wrap-reference.mjs",input);return new JSONObject(new String(input.readAllBytes(),StandardCharsets.UTF_8));
        }
    }
    private void cells(View view,List<TextView> result) {
        if(view instanceof TextView&&("markdown:th".equals(view.getTag())||"markdown:td".equals(view.getTag())))result.add((TextView)view);
        if(view instanceof ViewGroup)for(int i=0;i<((ViewGroup)view).getChildCount();i++)cells(((ViewGroup)view).getChildAt(i),result);
    }
    private void layout(View root,int width,int height) {
        root.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(height,View.MeasureSpec.EXACTLY));root.layout(0,0,width,height);
        for(int i=0;i<6;i++) {
            if(!root.getViewTreeObserver().dispatchOnPreDraw())return;
            root.measure(View.MeasureSpec.makeMeasureSpec(width,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(height,View.MeasureSpec.EXACTLY));root.layout(0,0,width,height);
        }
        fail("Table text origin must settle");
    }

    @Test public void actualWebLinesMatchAcrossMobileWidthsStyledWordsAccentsAndCjk() throws Exception {
        JSONObject reference=reference();JSONArray cases=reference.getJSONArray("cases"),fixtures=reference.getJSONArray("fixtures");
        JSONArray samples=new JSONArray();int lines=0,clicks=0;double maximumWidthError=0,maximumAdvanceError=0;
        for(String density:new String[]{"mdpi","420dpi","xxhdpi"})for(int f=0;f<fixtures.length();f++) {
            RuntimeEnvironment.setQualifiers("w412dp-h600dp-"+density);JSONObject fixture=fixtures.getJSONObject(f);
            try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui=new NativeUi(controller.get(),false,fixture.getString("locale"));ui.cjkFonts=NativeCjkFallbackTest.runtimeFonts();float scale=ui.context.getResources().getDisplayMetrics().density;
                FrameLayout root=new FrameLayout(ui.context);root.setBackgroundColor(ui.elevated);NativeMarkdownFlow flow=new NativeMarkdownFlow(ui);flow.collapseTop=flow.collapseBottom=false;flow.inset(18*scale,25*scale,18*scale,0);
                List<String> opened=new ArrayList<>();View markdown=NativeMarkdown.view(ui,fixture.getString("source"),false,opened::add);flow.add(markdown,0,0);root.addView(flow,new FrameLayout.LayoutParams(-1,-2));controller.get().setContentView(root);
                List<TextView> cells=new ArrayList<>();cells(markdown,cells);assertEquals(4,cells.size());List<Object> documents=new ArrayList<>();for(TextView cell:cells){documents.add(cell.getText());Selection.setSelection((Spannable)cell.getText(),0,cell.length());}
                for(int c=0;c<cases.length();c++) {
                    JSONObject item=cases.getJSONObject(c);if(item.getDouble("density")!=scale||!item.getString("id").equals(fixture.getString("id")))continue;
                    layout(root,ui.dp(item.getInt("width")),ui.dp(600));JSONArray measured=new JSONArray();
                    for(int i=0;i<cells.size();i++) {
                        TextView cell=cells.get(i);JSONObject expected=item.getJSONArray("cells").getJSONObject(i);assertSame(documents.get(i),cell.getText());assertEquals(0,cell.getSelectionStart());assertEquals(cell.length(),cell.getSelectionEnd());assertEquals(expected.getString("text"),cell.getText().toString());
                        JSONArray actualLines=new JSONArray();for(int line=0;line<cell.getLineCount();line++){actualLines.put(cell.getText().subSequence(cell.getLayout().getLineStart(line),cell.getLayout().getLineEnd(line)).toString().trim());lines++;}
                        assertEquals(item.getString("id")+"/"+scale+"/"+item.getInt("width")+"/"+i,expected.getJSONArray("lines").toString(),actualLines.toString());
                        double widthError=Math.abs(cell.getWidth()-expected.getDouble("width")*scale);maximumWidthError=Math.max(maximumWidthError,widthError);assertTrue("Unchanged table column bounds: "+widthError,widthError<=1);
                        double advance=NativeCssWrap.advance(cell.getText(),0,cell.length(),cell.getPaint(),scale),advanceError=Math.abs(advance-expected.getDouble("advance"));maximumAdvanceError=Math.max(maximumAdvanceError,advanceError);
                        assertTrue(item.getString("id")+"/"+i+" CSS intrinsic width error="+advanceError,advanceError<=1d/64);
                        assertEquals("Accessibility must expose the source, not inserted display breaks",cell.getText().toString(),cell.createAccessibilityNodeInfo().getText().toString());
                        assertTrue(cell.onTextContextMenuItem(android.R.id.copy));android.content.ClipboardManager clipboard=(android.content.ClipboardManager)ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);
                        assertEquals("Native selected-text copy must retain original spaces",cell.getText().toString(),clipboard.getPrimaryClip().getItemAt(0).getText().toString());
                        Selection.setSelection((Spannable)cell.getText(),0,cell.length());
                        if(i==2) {
                            int line=cell.getLineCount()-1,start=cell.getLayout().getLineStart(line);float x=cell.getTotalPaddingLeft()+cell.getLayout().getPrimaryHorizontal(Math.min(start+1,cell.length()-1));float y=cell.getTotalPaddingTop()+(cell.getLayout().getLineTop(line)+cell.getLayout().getLineBottom(line))/2f;
                            int before=opened.size();for(int action:new int[]{android.view.MotionEvent.ACTION_DOWN,android.view.MotionEvent.ACTION_UP}){android.view.MotionEvent event=android.view.MotionEvent.obtain(0,0,action,x,y,0);try{assertTrue(cell.getMovementMethod().onTouchEvent(cell,(Spannable)cell.getText(),event));}finally{event.recycle();}}
                            assertEquals(before+1,opened.size());assertEquals("https://example.com/table",opened.get(before));clicks++;Selection.setSelection((Spannable)cell.getText(),0,cell.length());
                        }
                        measured.put(new JSONObject().put("lines",actualLines).put("widthError",widthError).put("advanceError",advanceError).put("displaySoftBreaks",NativeCssWrap.active(cell)));
                    }
                    samples.put(new JSONObject().put("id",fixture.getString("id")).put("density",scale).put("width",item.getInt("width")).put("cells",measured));
                    if(scale==1&&item.getInt("width")==360&&("boundary".equals(fixture.getString("id"))||"mixed".equals(fixture.getString("id")))) {
                        for(TextView cell:cells)Selection.removeSelection((Spannable)cell.getText());
                        android.graphics.Bitmap bitmap=android.graphics.Bitmap.createBitmap(root.getWidth(),root.getHeight(),android.graphics.Bitmap.Config.ARGB_8888);
                        try(java.io.OutputStream stream=Files.newOutputStream(Path.of(System.getProperty("spool.ui.artifacts"),"native-table-wrap-api"+android.os.Build.VERSION.SDK_INT+"-"+fixture.getString("id")+".png"))) {
                            root.draw(new android.graphics.Canvas(bitmap));assertTrue(bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG,100,stream));
                        }finally{bitmap.recycle();}
                        for(TextView cell:cells)Selection.setSelection((Spannable)cell.getText(),0,cell.length());
                    }
                }
            }
        }
        assertEquals(cases.length(),samples.length());Files.writeString(Path.of(System.getProperty("spool.ui.artifacts"),"native-table-wrap-api"+android.os.Build.VERSION.SDK_INT+".json"),new JSONObject().put("tables",samples.length()).put("lines",lines).put("clicks",clicks).put("maximumWidthError",maximumWidthError).put("maximumAdvanceError",maximumAdvanceError).put("cases",samples).toString(2));
    }

    @Test public void softBreaksDoNotMutateDocumentSelectionSpansOrClipboardAndDisappearWhenWidened() {
        RuntimeEnvironment.setQualifiers("w360dp-h600dp-mdpi");try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);TextView cell=ui.text("A long linked label that wraps across several native text lines while keeping its original selection and link behavior.",14,ui.text);ui.preciseText(cell,true);cell.setTextSize(12.88f);cell.setTextIsSelectable(true);cell.setBreakStrategy(android.text.Layout.BREAK_STRATEGY_SIMPLE);cell.setHyphenationFrequency(android.text.Layout.HYPHENATION_FREQUENCY_NONE);
            NativeCssWrap.install(cell);controller.get().setContentView(cell,new FrameLayout.LayoutParams(208,-2));cell.requestFocus();Object document=cell.getText();String original=cell.getText().toString();Selection.setSelection((Spannable)cell.getText(),12,85);
            NativeCssWrap.width(cell,208);cell.measure(View.MeasureSpec.makeMeasureSpec(208,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(600,View.MeasureSpec.AT_MOST));cell.layout(0,0,208,cell.getMeasuredHeight());assertTrue(NativeCssWrap.active(cell));assertTrue(cell.getLayout().getText().toString().contains("\n"));
            assertSame(document,cell.getText());assertEquals(original,cell.getText().toString());assertEquals(12,cell.getSelectionStart());assertEquals(85,cell.getSelectionEnd());assertTrue(cell.onTextContextMenuItem(android.R.id.copy));
            android.content.ClipboardManager clipboard=(android.content.ClipboardManager)ui.context.getSystemService(android.content.Context.CLIPBOARD_SERVICE);assertEquals(original.substring(12,85),clipboard.getPrimaryClip().getItemAt(0).getText().toString());
            assertTrue("Copy restores the display transformation",cell.getLayout().getText().toString().contains("\n"));
            try { NativeCssWrap.source(cell,()->{assertEquals(original,cell.getLayout().getText().toString());throw new IllegalStateException("source action failed");});fail("Expected action exception"); }
            catch(IllegalStateException expected){assertEquals("source action failed",expected.getMessage());}
            assertTrue("Exceptions restore the display transformation",cell.getLayout().getText().toString().contains("\n"));
            android.view.accessibility.AccessibilityEvent event=android.view.accessibility.AccessibilityEvent.obtain(android.view.accessibility.AccessibilityEvent.TYPE_VIEW_TEXT_SELECTION_CHANGED);
            try{cell.onPopulateAccessibilityEvent(event);assertEquals(1,event.getText().size());assertEquals(original,event.getText().get(0).toString());}finally{event.recycle();}
            Selection.setSelection((Spannable)cell.getText(),12,85);assertTrue(cell.onTextContextMenuItem(android.R.id.shareText));
            android.content.Intent chooser=org.robolectric.Shadows.shadowOf(controller.get()).getNextStartedActivity();assertNotNull(chooser);assertEquals(android.content.Intent.ACTION_CHOOSER,chooser.getAction());
            android.content.Intent share=chooser.getParcelableExtra(android.content.Intent.EXTRA_INTENT);assertNotNull(share);assertEquals(android.content.Intent.ACTION_SEND,share.getAction());assertEquals(original.substring(12,85),share.getCharSequenceExtra(android.content.Intent.EXTRA_TEXT).toString());
            assertTrue("Share restores the display transformation",cell.getLayout().getText().toString().contains("\n"));
            Selection.setSelection((Spannable)cell.getText(),12,85);
            NativeCssWrap.width(cell,1000);cell.measure(View.MeasureSpec.makeMeasureSpec(1000,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(600,View.MeasureSpec.AT_MOST));cell.layout(0,0,1000,cell.getMeasuredHeight());assertFalse(NativeCssWrap.active(cell));assertEquals(1,cell.getLineCount());assertSame(document,cell.getText());assertEquals(85,cell.getSelectionEnd());
        }
    }

    @Test public void complexBreakingAndAtomicCodeRemainOnTheNativeFallbackWithoutRemovingSourceCharacters() {
        RuntimeEnvironment.setQualifiers("w360dp-h600dp-mdpi");try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false);for(String source:new String[]{"| A | B |\n| --- | --- |\n| [some `code` and words](https://example.com) | 1 |","| A | B |\n| --- | --- |\n| مرحبا بك في النص العربي | 1 |","| A | B |\n| --- | --- |\n| long-word-with-hyphens remains native | 1 |"}) {
                View markdown=NativeMarkdown.view(ui,source,false,value->{});List<TextView> cells=new ArrayList<>();cells(markdown,cells);TextView cell=cells.get(2);Object document=cell.getText();String text=cell.getText().toString();NativeCssWrap.width(cell,60);assertFalse(NativeCssWrap.active(cell));assertSame(document,cell.getText());assertEquals(text,cell.getText().toString());
            }
        }
    }

    @Test public void recordNativeFractionalGlyphAdvancesForActualWebComparison() throws Exception {
        JSONArray cases = new JSONArray();
        for(String density:new String[]{"mdpi","420dpi","xxhdpi"}) {
            RuntimeEnvironment.setQualifiers("w360dp-h600dp-"+density);
            try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
                NativeUi ui=new NativeUi(controller.get(),false,"en");TextView cell=ui.text("",14,ui.text);ui.preciseText(cell,true);
                float scale=controller.get().getResources().getDisplayMetrics().density;
                for(float size:new float[]{12.875f,12.88f,12.890625f,13})for(boolean linear:new boolean[]{false,true})for(int hinting:new int[]{Paint.HINTING_OFF,Paint.HINTING_ON})for(int normalization:new int[]{1,1024}) {
                    Paint paint=new Paint(cell.getPaint());paint.setTextSize((normalization==1?size*scale:(float)Math.floor(size*100)/100*normalization));paint.setLinearText(linear);paint.setHinting(hinting);
                    for(String phrase:new String[]{"A long linked label that wraps","across several native text lines while","keeping its original selection and"}) {
                        char[] chars=phrase.toCharArray();float[] advances=new float[chars.length];paint.getTextRunAdvances(chars,0,chars.length,0,chars.length,false,advances,0);
                        JSONArray positions=new JSONArray().put(0);float x=0;for(float advance:advances){x+=advance;positions.put(x);}
                        float run=paint.getRunAdvance(phrase,0,phrase.length(),0,phrase.length(),false,phrase.length());assertTrue(Float.isFinite(run)&&run>0);
                        cases.put(new JSONObject().put("density",scale).put("size",size).put("normalization",normalization).put("linear",linear).put("hinting",hinting).put("text",phrase).put("advance",run).put("measured",paint.measureText(phrase)).put("positions",positions));
                    }
                }
            }
        }
        Files.writeString(Path.of(System.getProperty("spool.ui.artifacts"),"native-table-wrap-metrics-api"+android.os.Build.VERSION.SDK_INT+".json"),new JSONObject().put("scope","Diagnostic native Paint advances; not a Web/line/raster pass").put("cases",cases).toString(2));
    }
}
