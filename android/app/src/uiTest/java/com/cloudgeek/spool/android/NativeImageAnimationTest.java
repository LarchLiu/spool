package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.graphics.Bitmap;
import android.app.Activity;
import android.graphics.Canvas;
import android.os.Looper;
import android.view.View;
import android.widget.FrameLayout;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import java.nio.charset.StandardCharsets;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.Robolectric;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;

@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
public class NativeImageAnimationTest {
    private byte[] fixture(String name) throws Exception { try (java.io.InputStream input = getClass().getResourceAsStream("/image-animation/" + name)) { assertNotNull(name, input); return input.readAllBytes(); } }
    @Test public void allNativeFramesDisposalBlendTimingAndLoopCountsMatchActualChrome() throws Exception {
        JSONArray files = new JSONArray(new String(fixture("web-frames.json"), StandardCharsets.UTF_8));
        Bitmap contact=Bitmap.createBitmap(800,files.length()*126,Bitmap.Config.ARGB_8888); Canvas sheet=new Canvas(contact); sheet.drawColor(0xffeeeeee); android.graphics.Paint label=new android.graphics.Paint(android.graphics.Paint.ANTI_ALIAS_FLAG); label.setColor(0xff222222); label.setTypeface(android.graphics.Typeface.MONOSPACE); label.setTextSize(14);
        for (int i = 0; i < files.length(); i++) {
            JSONObject file = files.getJSONObject(i); String name = file.getString("name"); NativeImageSequence sequence = NativeImageSequence.parse(fixture(name));
            sheet.drawText(name,8,i*126+18,label);
            assertNotNull(name, sequence); assertEquals(name, file.getInt("loops"), sequence.loops); JSONArray frames = file.getJSONArray("frames"); assertEquals(name, frames.length(), sequence.frames.size());
            try (NativeImageSequence.Renderer renderer = sequence.renderer(2048)) {
                for (int loop = 0; loop < 2; loop++) for (int frame = 0; frame < frames.length(); frame++) {
                    JSONObject expected = frames.getJSONObject(frame); int duration=expected.getInt("duration"); assertEquals(name, duration<=10?100:duration, sequence.frames.get(frame).delay);
                    renderer.render(frame); Bitmap actual = renderer.surface; assertPixels(name+" frame "+frame,actual,expected);
                    if(loop==0) sheet.drawBitmap(actual,null,new android.graphics.Rect(frame*160,i*126+24,frame*160+128,i*126+120),null);
                    if(loop==0&&frame==0) {
                        Bitmap poster=renderer.snapshot(); assertPixels(name+" oriented poster",poster,file.getJSONObject("poster")); sheet.drawBitmap(poster,null,new android.graphics.Rect(640,i*126+24,768,i*126+120),null); poster.recycle();
                    }
                }
            }
        }
        java.io.File output=new java.io.File(System.getProperty("spool.ui.artifacts")); assertTrue(output.isDirectory()||output.mkdirs());
        try(java.io.FileOutputStream stream=new java.io.FileOutputStream(new java.io.File(output,"native-image-animation-contact-"+android.os.Build.VERSION.SDK_INT+".png"))) { assertTrue(contact.compress(Bitmap.CompressFormat.PNG,100,stream)); } contact.recycle();
    }
    private void assertPixels(String name,Bitmap actual,JSONObject expected) throws Exception {
        assertEquals(name,expected.getInt("width"),actual.getWidth()); assertEquals(name,expected.getInt("height"),actual.getHeight()); byte[] rgba=android.util.Base64.decode(expected.getString("pixels"),android.util.Base64.DEFAULT);
        for(int y=0;y<actual.getHeight();y++) for(int x=0;x<actual.getWidth();x++) { int pixel=actual.getPixel(x,y),at=(y*actual.getWidth()+x)*4,alpha=rgba[at+3]&255;
            assertEquals(name+" alpha at "+x+","+y,alpha,android.graphics.Color.alpha(pixel));
            if(alpha!=0) for(int c=0;c<3;c++) assertEquals(name+" pixel at "+x+","+y,rgba[at+c]&255,(pixel>>>((2-c)*8))&255,1);
        }
    }
    @Test public void malformedContainersBoundsAndTruncatedFramesFailClosedBeforeRasterAllocation() throws Exception {
        for (String name : new String[] {"restore.gif","blend.webp"}) {
            byte[] valid = fixture(name);
            for (int length : new int[] {12,20,valid.length-1}) assertThrows(java.io.IOException.class, () -> NativeImageSequence.parse(java.util.Arrays.copyOf(valid,length)));
            byte[] oversized = valid.clone();
            if (name.endsWith("gif")) { oversized[6] = oversized[7] = oversized[8] = oversized[9] = (byte) 255; }
            else { for (int offset=24;offset<30;offset++) oversized[offset] = (byte) 255; }
            assertThrows(java.io.IOException.class, () -> NativeImageSequence.parse(oversized));
        }
        assertNull(NativeImageSequence.parse(new byte[] {1,2,3}));
        byte[] gif = fixture("restore.gif"); NativeImageSequence sequence = NativeImageSequence.parse(gif); NativeImageSequence.Frame first = sequence.frames.get(0);
        java.io.ByteArrayOutputStream bomb = new java.io.ByteArrayOutputStream(); bomb.write(gif,0,first.offset-1);
        for(int i=0;i<=NativeImageSequence.MAX_FRAMES;i++) { bomb.write(0x2c); bomb.write(gif,first.offset,first.length); }
        bomb.write(0x3b); assertThrows(java.io.IOException.class, () -> NativeImageSequence.parse(bomb.toByteArray()));
        try(NativeImageSequence.Renderer scaled=sequence.renderer(4)) { scaled.render(0); assertEquals(4,scaled.surface.getWidth()); assertEquals(3,scaled.surface.getHeight()); Bitmap snapshot=scaled.snapshot(); assertEquals(android.util.DisplayMetrics.DENSITY_DEFAULT,snapshot.getDensity()); snapshot.recycle(); }
    }

    private void idle(long millis) { Shadows.shadowOf(Looper.getMainLooper()).idleFor(java.time.Duration.ofMillis(millis)); }
    private void flush(List<Runnable> jobs) { for (int limit=0;!jobs.isEmpty();limit++) { assertTrue("No unbounded decode loop", limit<100); jobs.remove(0).run(); Shadows.shadowOf(Looper.getMainLooper()).idle(); } }
    private Bitmap draw(NativeAnimatedImage image) { Bitmap pixels=Bitmap.createBitmap(8,6,Bitmap.Config.ARGB_8888); image.setBounds(0,0,8,6); image.draw(new Canvas(pixels)); return pixels; }
    private void layout(View view) { view.measure(View.MeasureSpec.makeMeasureSpec(360,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(800,View.MeasureSpec.EXACTLY)); view.layout(0,0,360,800); }

    @Test public void playbackUsesIndependentBoundedWorkerJobsAndHonorsFrameDelaysPauseAndFiniteRepeats() throws Exception {
        NativeImageSequence sequence=NativeImageSequence.parse(fixture("restore.gif")); List<Runnable> jobs=new ArrayList<>();
        try(NativeImageSequence.Renderer renderer=sequence.renderer(8)) {
            renderer.render(0); Bitmap first=renderer.snapshot(); NativeAnimatedImage animation=new NativeAnimatedImage(sequence,first,8,1,jobs::add);
            animation.start(); assertTrue(animation.isRunning()); idle(79); assertTrue(jobs.isEmpty()); idle(1); assertEquals(1,jobs.size());
            idle(500); assertEquals("Slow workers do not enqueue more frames",1,jobs.size()); flush(jobs); renderer.render(1); Bitmap pixels=draw(animation); assertTrue(renderer.surface.sameAs(pixels)); pixels.recycle();
            animation.stop(); idle(1000); assertTrue(jobs.isEmpty()); animation.start(); idle(119); assertTrue(jobs.isEmpty()); idle(1); flush(jobs);
            renderer.render(2); pixels=draw(animation); assertTrue(renderer.surface.sameAs(pixels)); pixels.recycle();
            for(int loop=0;loop<sequence.loops;loop++) for(int frame=loop==0?2:0;frame<sequence.frames.size();frame++) { idle(sequence.frames.get(frame).delay); flush(jobs); }
            assertFalse("Finite GIF repetition must stop on its final frame",animation.isRunning()); animation.start(); assertFalse(animation.isRunning()); idle(1000); assertTrue(jobs.isEmpty());
            animation.release(true); flush(jobs); pixels=draw(animation); assertEquals(0,pixels.getPixel(0,0)); pixels.recycle(); first.recycle();
        }
    }

    @Test public void coalescedImagesHaveIndependentPlaybackAndVisibilityDetachRebindAndLogoutFenceDecodeResults() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi"); List<Runnable> jobs=new ArrayList<>(); AtomicInteger reads=new AtomicInteger(); byte[] bytes=fixture("blend.webp");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false); NativeImages images=new NativeImages(jobs::add,path->{reads.incrementAndGet();return new NativeFileData("image/webp",bytes);});
            FrameLayout root=new FrameLayout(controller.get()); NativeImageView first=new NativeImageView(controller.get()),second=new NativeImageView(controller.get()); root.addView(first,new FrameLayout.LayoutParams(80,60)); root.addView(second,new FrameLayout.LayoutParams(80,60)); controller.get().setContentView(root); layout(root);
            String path=NativeFileData.attachmentPath("chat","animation"); images.remote(first,path,80,null); images.remote(second,path,80,null); assertEquals(1,jobs.size()); flush(jobs); layout(root); root.getViewTreeObserver().dispatchOnPreDraw();
            assertEquals(1,reads.get()); assertTrue(first.getDrawable() instanceof NativeAnimatedImage); assertNotSame(first.getDrawable(),second.getDrawable());
            NativeAnimatedImage a=(NativeAnimatedImage)first.getDrawable(),b=(NativeAnimatedImage)second.getDrawable(); assertTrue(a.isRunning()); assertTrue(b.isRunning());
            first.setVisibility(View.GONE); assertFalse(a.isRunning()); assertTrue(b.isRunning()); idle(80); assertEquals(1,jobs.size()); flush(jobs);
            first.setVisibility(View.VISIBLE); layout(root); root.getViewTreeObserver().dispatchOnPreDraw(); assertTrue(a.isRunning());
            first.setTranslationY(900); root.getViewTreeObserver().dispatchOnPreDraw(); assertFalse("Clipped, offscreen cards must not decode forever",a.isRunning()); first.setTranslationY(0); root.getViewTreeObserver().dispatchOnPreDraw(); assertTrue(a.isRunning());
            root.removeView(first); assertFalse(a.isRunning()); flush(jobs); root.addView(first,new FrameLayout.LayoutParams(80,60)); layout(root); root.getViewTreeObserver().dispatchOnPreDraw(); assertTrue(a.isRunning());
            NativeImageView cached=new NativeImageView(controller.get()); images.remote(cached,path,80,null); assertEquals(1,reads.get()); assertTrue(cached.getDrawable() instanceof NativeAnimatedImage);
            idle(80); assertFalse(jobs.isEmpty()); images.clear(); flush(jobs); idle(1000); flush(jobs); assertFalse(a.isRunning()); assertFalse(b.isRunning());
            Bitmap pixels=draw(a); assertEquals(0,pixels.getPixel(0,0)); pixels.recycle();
            images.remote(first,path,80,null); assertEquals(1,jobs.size()); images.clear(); flush(jobs); assertEquals("Queued revoked loads must not call the transport",1,reads.get()); assertNull(first.getDrawable());
        }
    }

    @Test public void staleWorkerReleaseCannotCloseARestartedRendererAndRebindingCancelsThePreviousView() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi"); List<Runnable> jobs=new ArrayList<>(); NativeImageSequence sequence=NativeImageSequence.parse(fixture("blend.webp"));
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup(); NativeImageSequence.Renderer renderer=sequence.renderer(8)) {
            renderer.render(0); Bitmap first=renderer.snapshot(); NativeAnimatedImage animation=new NativeAnimatedImage(sequence,first,8,1,jobs::add); animation.start(); idle(80); flush(jobs);
            animation.release(false); assertEquals(1,jobs.size()); Runnable oldClose=jobs.remove(0); animation.start(); idle(80); assertEquals(1,jobs.size()); flush(jobs); oldClose.run();
            idle(120); flush(jobs); assertTrue(animation.isRunning()); renderer.render(1); renderer.render(2); Bitmap current=draw(animation); assertTrue(renderer.surface.sameAs(current)); current.recycle();
            NativeImageView view=new NativeImageView(controller.get()); view.setImageDrawable(animation); animation.start(); assertTrue(animation.isRunning()); view.setImageDrawable(null); assertFalse(animation.isRunning()); flush(jobs); idle(500); flush(jobs); animation.release(true); flush(jobs); first.recycle();
        }
    }

    @Test public void attachmentModalAndInlineFilePreviewUseTheSameNativeAnimationWithoutChangingActions() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-mdpi"); List<Runnable> jobs=new ArrayList<>(); AtomicInteger opens=new AtomicInteger(),downloads=new AtomicInteger(); byte[] bytes=fixture("blend.webp");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false); NativeImages images=new NativeImages(jobs::add,path->new NativeFileData("image/webp",bytes));
            android.widget.LinearLayout page=ui.column(); controller.get().setContentView(page);
            NativeAttachmentCard card=new NativeAttachmentCard(ui,"motion.webp","image/webp","",null,opens::incrementAndGet,null); page.addView(card);
            NativeImagePreview.Content modal=new NativeImagePreview.Content(ui,"motion.webp",downloads::incrementAndGet,()->{}); page.addView(modal,new android.widget.LinearLayout.LayoutParams(-1,-2));
            NativeFilePreview file=new NativeFilePreview(ui,"mobile",images,target->{},url->{},value->{},()->{}); page.addView(file,new android.widget.LinearLayout.LayoutParams(-1,300));
            String path=NativeFileData.attachmentPath("chat","animated"); images.remote(card.image,path,80,null); images.remote(modal.image,path,80,modal::loaded);
            file.bind(new JSONObject().put("kind","image").put("name","motion.webp").put("path","motion.webp").put("size",bytes.length),new NativeFileTarget("motion.webp",0,0),"");
            flush(jobs); layout(page); page.getViewTreeObserver().dispatchOnPreDraw();
            assertTrue(card.image.getDrawable() instanceof NativeAnimatedImage); assertTrue(modal.image.getDrawable() instanceof NativeAnimatedImage);
            assertTrue(findImage(file.scroll).getDrawable() instanceof NativeAnimatedImage); assertEquals(View.GONE,modal.status.getVisibility()); assertEquals(View.VISIBLE,modal.image.getVisibility());
            assertEquals(8,modal.image.getWidth()); assertEquals(6,modal.image.getHeight()); card.open.performClick(); modal.download.performClick(); assertEquals(1,opens.get()); assertEquals(1,downloads.get());
            images.clear(); flush(jobs);
        }
    }
    @Test public void denseScreenAnimationRetainsCssIntrinsicSizeWithoutUpscalingDecodedPixels() throws Exception {
        RuntimeEnvironment.setQualifiers("w360dp-h800dp-xhdpi"); List<Runnable> jobs=new ArrayList<>(); byte[] bytes=fixture("restore.gif");
        try(ActivityController<Activity> controller=Robolectric.buildActivity(Activity.class).setup()) {
            NativeUi ui=new NativeUi(controller.get(),false); NativeImages images=new NativeImages(jobs::add,path->new NativeFileData("image/gif",bytes)); NativeContainedImage view=new NativeContainedImage(ui,false); FrameLayout page=new FrameLayout(controller.get()); page.addView(view,new FrameLayout.LayoutParams(-2,-2)); controller.get().setContentView(page);
            images.remote(view,NativeFileData.attachmentPath("chat","gif"),80,null); flush(jobs); layout(page); page.getViewTreeObserver().dispatchOnPreDraw(); assertEquals(ui.dp(8),view.getWidth()); assertEquals(ui.dp(6),view.getHeight()); assertTrue(((NativeAnimatedImage)view.getDrawable()).isRunning());
            images.clear(); flush(jobs);
        }
    }
    private android.widget.ImageView findImage(View root) {
        if(root instanceof android.widget.ImageView) return (android.widget.ImageView)root;
        if(root instanceof android.view.ViewGroup) for(int i=0;i<((android.view.ViewGroup)root).getChildCount();i++) { android.widget.ImageView result=findImage(((android.view.ViewGroup)root).getChildAt(i)); if(result!=null) return result; }
        return null;
    }
}
