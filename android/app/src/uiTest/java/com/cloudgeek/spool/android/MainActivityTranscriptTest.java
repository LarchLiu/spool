package com.cloudgeek.spool.android;

import static org.junit.Assert.*;
import android.os.Looper;
import android.text.Selection;
import android.text.Spannable;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.GraphicsMode;
import org.robolectric.annotation.LooperMode;
import org.robolectric.util.ReflectionHelpers;

/** Keyed production Activity updates; only the Go transport boundary is shadowed. */
@RunWith(org.robolectric.RobolectricTestRunner.class)
@Config(sdk = {26,35}, shadows = {MainActivityGoalTest.ShadowClient.class, MainActivityGoalTest.ShadowSeq.class})
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
public class MainActivityTranscriptTest {
    private JSONObject chat() throws Exception {
        JSONArray turns = new JSONArray(), parts = new JSONArray();
        for (int i = 0; i < 2; i++) {
            parts.put(new JSONObject().put("role","user").put("kind","text").put("text", i == 0 ? "Long prompt\n".repeat(16) : "Next prompt"));
            parts.put(new JSONObject().put("role","assistant").put("kind","text").put("phase","final_answer").put("text","Selectable answer " + i));
            turns.put(new JSONObject().put("id","turn-"+i).put("user_id","alice").put("backend","codex").put("status", i == 0 ? "completed" : "running")
                    .put("started_at",1000).put("finished_at", i == 0 ? 2000 : 0).put("part_start",i*2).put("input_end",i*2+1).put("part_end",i*2+2));
        }
        return new JSONObject().put("id","chat-one").put("title","Title").put("project_id","project-one").put("backend","codex").put("can_control",true)
                .put("turns",turns).put("status","running").put("transcript",new JSONObject().put("parts",parts));
    }
    private void layout(MainActivityGoalTest.Harness h) {
        View root = h.get("root"); root.measure(View.MeasureSpec.makeMeasureSpec(360,View.MeasureSpec.EXACTLY),View.MeasureSpec.makeMeasureSpec(800,View.MeasureSpec.EXACTLY));
        root.layout(0,0,360,800); root.getViewTreeObserver().dispatchOnPreDraw();
    }
    private void show(MainActivityGoalTest.Harness h, JSONObject value) { h.set("chat",value); h.call("renderChat"); layout(h); }

    @Test public void providerOnlyInputHasNoEmptyUserRowAndCompactionRebindsWithoutTextChanges() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value = chat();
            JSONArray parts = value.getJSONObject("transcript").getJSONArray("parts");
            parts.getJSONObject(0).put("role", "assistant").put("text", "Historical answer");
            JSONObject turn = value.getJSONArray("turns").getJSONObject(1);
            turn.put("compacting", false);
            show(h, value);
            assertEquals(1, views(h.get("historyContent"), NativeUserRow.class).size());
            NativeTurn response = views(h.get("historyContent"), NativeTurn.class).get(1);
            assertFalse(response.activity.summaryLabel.getText().toString().contains("Compacting context"));
            turn.put("compacting", true);
            show(h, value);
            assertSame(response, views(h.get("historyContent"), NativeTurn.class).get(1));
            assertTrue(response.activity.summaryLabel.getText().toString().contains("Compacting context"));
            turn.put("compacting", false);
            show(h, value);
            assertFalse(response.activity.summaryLabel.getText().toString().contains("Compacting context"));
        }
    }
    private <T extends View> List<T> views(View parent, Class<T> type) {
        List<T> result = new ArrayList<>();
        if (type.isInstance(parent)) result.add(type.cast(parent));
        if (parent instanceof ViewGroup) for(int i=0;i<((ViewGroup)parent).getChildCount();i++) result.addAll(views(((ViewGroup)parent).getChildAt(i),type));
        return result;
    }
    private TextView text(View parent, String value) {
        for (TextView text : views(parent,TextView.class)) if (text.getText().toString().startsWith(value)) return text;
        throw new AssertionError("Missing text " + value);
    }

    @Test public void settingsOverlayKeepsTranscriptEditorSelectionAndScrollTree() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, chat()); View root = h.get("root"), history = h.get("history"), composer = h.get("composer");
            LinearLayout content = h.get("historyContent"); NativeUserRow first = views(content, NativeUserRow.class).get(0);
            first.message.toggle.performClick(); layout(h);
            TextView answer = text(content, "Selectable answer"); Object document = answer.getText();
            Selection.setSelection((Spannable) document, 2, 7);
            h.call("openSettings"); NativeSettings dialog = h.get("settingsDialog"); assertTrue(dialog.isShowing());
            assertSame(root, h.get("root")); assertSame(history, h.get("history")); assertSame(composer, h.get("composer"));
            assertSame(document, answer.getText()); assertEquals(7, answer.getSelectionEnd());
            h.call("openSettings"); assertSame(dialog, h.get("settingsDialog"));
            dialog.content.close.performClick();
            assertNull(h.get("settingsDialog")); assertNull(h.get("connectionSettings")); assertFalse(h.<Boolean>get("settingsOpen"));
            assertSame(root, h.get("root")); assertSame(content, h.get("historyContent")); assertSame(composer, h.get("composer"));
            assertSame(document, answer.getText()); assertEquals(2, answer.getSelectionStart()); assertTrue(first.message.content.expanded);
            assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void scrollingUserAndAgentMessagesNeverPaintOverTheWorkspaceHeader() throws Exception {
        for (String theme : new String[] {"light", "dark"}) try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("theme", theme); h.call("render");
            JSONObject value = chat();
            value.getJSONObject("transcript").getJSONArray("parts").getJSONObject(1).put("text", "Agent response\n\n".repeat(80));
            show(h, value);
            views(h.get("historyContent"), NativeUserRow.class).get(0).message.toggle.performClick();
            layout(h);
            View root = h.get("root");
            androidx.core.widget.NestedScrollView history = h.get("history");
            NativeUi.Header header = h.get("workspaceHeader");
            int[] expected = null;
            for (int top : new int[] {0, 40, 100, 250, 500, 800, 1200}) {
                history.scrollTo(0, top);
                android.graphics.Bitmap image = android.graphics.Bitmap.createBitmap(root.getWidth(), root.getHeight(), android.graphics.Bitmap.Config.ARGB_8888);
                try {
                    root.draw(new android.graphics.Canvas(image));
                    int[] pixels = new int[root.getWidth() * (header.getHeight() + 1)];
                    image.getPixels(pixels, 0, root.getWidth(), 0, 0, root.getWidth(), header.getHeight() + 1);
                    if (expected == null) expected = pixels;
                    else assertArrayEquals(theme + " header changed at scroll " + top, expected, pixels);
                } finally { image.recycle(); }
            }
            assertTrue("The fixture must actually scroll past both message types", history.getScrollY() >= 1200);
        }
    }

    @Test public void unchangedComposerAndPresenceUpdatesKeepTextHintsIconsAndAvatarTrees() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h, chat()); NativeUi.Composer composer = h.composer(); NativeUi.Header header = h.get("workspaceHeader");
            Object hint = composer.input.getHint(), label = composer.model.label.getText(), icon = composer.model.symbol.getDrawable();
            View people = header.online.getChildAt(0);
            for (int i = 0; i < 8; i++) { h.call("updateComposer"); h.call("updateHeaderPeople"); layout(h); }
            assertSame(hint, composer.input.getHint()); assertSame(label, composer.model.label.getText());
            assertSame(icon, composer.model.symbol.getDrawable()); assertSame(people, header.online.getChildAt(0));
        }
    }

    @Test public void productionLongTranscriptUsesViewportRowsAndRebindsOffscreenDataOnReturn() throws Exception {
        try (MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value = chat(); JSONArray parts = new JSONArray(), turns = new JSONArray();
            for (int i = 0; i < 100; i++) {
                parts.put(new JSONObject().put("role", "user").put("kind", "text").put("text", "Prompt " + i));
                parts.put(new JSONObject().put("role", "assistant").put("kind", "text").put("phase", "final_answer").put("text", "Answer " + i));
                turns.put(new JSONObject().put("id", "long-" + i).put("status", "completed").put("part_start", i * 2).put("input_end", i * 2 + 1).put("part_end", i * 2 + 2));
            }
            value.put("status", "completed").put("turns", turns).put("transcript", new JSONObject().put("parts", parts));
            show(h, value); for (int i = 0; i < 8; i++) layout(h);
            LinearLayout content = h.get("historyContent"); assertEquals(100, content.getChildCount());
            assertTrue(content.getChildAt(0) instanceof NativeTranscriptViewport.Row);
            assertTrue("Only visible user rows are mounted", views(content, NativeUserRow.class).size() < 20);
            parts.getJSONObject(1).put("text", "Updated offscreen answer"); h.call("renderChat");
            for (int i = 0; i < 8; i++) layout(h);
            h.<androidx.core.widget.NestedScrollView>get("history").scrollTo(0, 0); for (int i = 0; i < 8; i++) layout(h);
            assertNotNull(text(content, "Updated offscreen answer"));
            assertEquals(200, h.<JSONObject>get("chat").getJSONObject("transcript").getJSONArray("parts").length());
            assertTrue(views(content, NativeUserRow.class).size() < 20);
            NativeUserRow first = views(content, NativeUserRow.class).get(0);
            TextView answer = text(content, "Updated offscreen answer"); Object document = answer.getText();
            h.<androidx.core.widget.NestedScrollView>get("history").scrollTo(0, 2000); for (int i = 0; i < 8; i++) layout(h);
            assertFalse(first.isAttachedToWindow());
            h.<androidx.core.widget.NestedScrollView>get("history").scrollTo(0, 0); for (int i = 0; i < 8; i++) layout(h);
            assertSame(first, views(content, NativeUserRow.class).get(0));
            assertSame(answer, text(content, "Updated offscreen answer")); assertSame(document, answer.getText());
        }
    }

    @Test public void streamingAndMetadataKeepOtherDocumentsExpansionSelectionAndCopyFeedback() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value = chat(); show(h,value);
            LinearLayout content=h.get("historyContent"); List<NativeUserRow> users=views(content,NativeUserRow.class); List<NativeTurn> responses=views(content,NativeTurn.class);
            NativeUserRow first=users.get(0); first.message.toggle.performClick(); layout(h); assertTrue(first.message.content.expanded);
            TextView selected=text(first,"Long prompt"); Object document=selected.getText(); Selection.setSelection((Spannable)document,2,8);
            TextView answer=text(responses.get(0),"Selectable answer"); Object answerDocument=answer.getText(); Selection.setSelection((Spannable)answerDocument,1,5);
            first.message.footer.copy.performClick(); assertEquals("Message copied",first.message.footer.copy.getContentDescription());
            value.getJSONObject("transcript").getJSONArray("parts").getJSONObject(3).put("text","New streaming answer");
            show(h,value); assertEquals(users,views(content,NativeUserRow.class)); assertEquals(responses,views(content,NativeTurn.class));
            assertSame(document,selected.getText()); assertEquals(2,selected.getSelectionStart()); assertEquals(8,selected.getSelectionEnd());
            assertSame(answerDocument,answer.getText()); assertEquals(1,answer.getSelectionStart()); assertTrue(first.message.content.expanded);
            assertEquals("Message copied",first.message.footer.copy.getContentDescription());
            JSONObject clone=new JSONObject(value.toString()).put("title","Metadata only").put("updated_at",99);
            clone.getJSONArray("turns").getJSONObject(0).put("model","new-model"); show(h,clone);
            assertSame(responses.get(0),views(content,NativeTurn.class).get(0)); assertSame(answerDocument,answer.getText());
            assertSame(document,selected.getText()); assertEquals("Message copied",first.message.footer.copy.getContentDescription());
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(2001)); assertEquals("Copy message",first.message.footer.copy.getContentDescription());
        }
    }

    @Test public void historyRebasesPartsWithoutDetachingExistingNodesOrReplacingTheirDocuments() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value=chat(); show(h,value); LinearLayout content=h.get("historyContent");
            NativeUserRow first=views(content,NativeUserRow.class).get(0); NativeTurn response=views(content,NativeTurn.class).get(0);
            TextView answer=text(response,"Selectable answer"); Object document=answer.getText(); Selection.setSelection((Spannable)document,2,6);
            java.util.concurrent.atomic.AtomicInteger detached=new java.util.concurrent.atomic.AtomicInteger();
            first.addOnAttachStateChangeListener(new View.OnAttachStateChangeListener(){public void onViewAttachedToWindow(View view){} public void onViewDetachedFromWindow(View view){detached.incrementAndGet();}});
            JSONObject older=chat(); older.getJSONArray("turns").getJSONObject(0).put("id","older-0"); older.getJSONArray("turns").getJSONObject(1).put("id","older-1");
            show(h,NativeChatState.prepend(value,older));
            assertSame(first,views(content,NativeUserRow.class).get(2)); assertSame(response,views(content,NativeTurn.class).get(2));
            assertSame(document,answer.getText()); assertEquals(2,answer.getSelectionStart()); assertEquals(0,detached.get());
        }
    }

    @Test public void profileRefreshRebindsNameAndLocalAvatarWithoutLosingSelectionOrCopyState() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            h.set("user",new JSONObject().put("id","alice").put("nickname","Original").put("avatar",NativeAvatars.options("clay","one")));
            JSONObject value=chat(); show(h,value); NativeUserRow row=views(h.get("historyContent"),NativeUserRow.class).get(0);
            TextView selected=text(row,"Long prompt"); Object document=selected.getText(); Selection.setSelection((Spannable)document,2,7);
            row.message.footer.copy.performClick(); View avatar=row.getChildAt(1);
            JSONObject update=new JSONObject().put("revision",5).put("people",new JSONArray().put(new JSONObject().put("id","alice").put("nickname","Renamed").put("avatar",NativeAvatars.options("pixel-art","two"))));
            ReflectionHelpers.callInstanceMethod(h.activity,"workspaceEvent",ReflectionHelpers.ClassParameter.from(String.class,"profiles"),ReflectionHelpers.ClassParameter.from(String.class,update.toString()),ReflectionHelpers.ClassParameter.from(int.class,h.<Integer>get("workspaceEpoch")));
            layout(h); assertSame(row,views(h.get("historyContent"),NativeUserRow.class).get(0)); assertSame(avatar,row.getChildAt(1));
            assertEquals("Renamed",row.message.footer.author.getText().toString()); assertEquals("Renamed",row.getContentDescription());
            assertTrue(avatar.getTag().toString().contains("pixel-art")); assertSame(document,selected.getText()); assertEquals(7,selected.getSelectionEnd());
            assertEquals("Message copied",row.message.footer.copy.getContentDescription()); assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void terminalMetadataKeepsUnchangedAnswerAndUpdatesTimestampButChangedTextIsNeverStale() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value=chat(); show(h,value); NativeTurn response=views(h.get("historyContent"),NativeTurn.class).get(1);
            NativeMessage message=views(response,NativeMessage.class).get(0); TextView selected=text(message,"Selectable answer 1"); Object document=selected.getText();
            Selection.setSelection((Spannable)document,2,8); message.footer.copy.performClick();
            value.getJSONArray("turns").getJSONObject(1).put("status","completed").put("finished_at",3000); show(h,value);
            assertSame(message,views(response,NativeMessage.class).get(0)); assertSame(document,selected.getText()); assertEquals(8,selected.getSelectionEnd());
            assertEquals("Message copied",message.footer.copy.getContentDescription()); assertSame(message.footer,message.footer.time.getParent());
            value.getJSONObject("transcript").getJSONArray("parts").getJSONObject(3).put("text","Actually changed answer"); show(h,value);
            NativeMessage replacement=views(response,NativeMessage.class).get(0); assertSame("Web retains the answer/footer when its text changes",message,replacement);
            assertEquals("Message copied",replacement.footer.copy.getContentDescription());
            replacement.footer.copy.performClick(); android.content.ClipboardManager clipboard=(android.content.ClipboardManager)h.activity.getSystemService(android.content.Context.CLIPBOARD_SERVICE);
            assertEquals("Actually changed answer",clipboard.getPrimaryClip().getItemAt(0).getText().toString());
        }
    }

    @Test public void realActivityToolSnapshotsKeepTheirMountedInputAndOutputUntilFinalAnswerCollapsesProcess() throws Exception {
        try(MainActivityGoalTest.Harness h=new MainActivityGoalTest.Harness(true)) {
            JSONObject value=chat();JSONArray parts=value.getJSONObject("transcript").getJSONArray("parts");JSONObject turn=value.getJSONArray("turns").getJSONObject(1);
            parts.put(3,new JSONObject().put("role","assistant").put("kind","tool_use").put("id","read-one").put("name","read_file").put("text","{\"file_path\":\"src/example.ts\"}"));show(h,value);
            NativeTurn response=views(h.get("historyContent"),NativeTurn.class).get(1);NativeActivityList activity=response.activity;
            TextView title=text(activity,"Used Read file");((View)title.getParent()).performClick();layout(h);NativeToolDetail detail=views(activity,NativeToolDetail.class).get(0);TextView input=text(detail,"{\"file_path\"");Object document=input.getText();Selection.setSelection((Spannable)document,1,6);
            parts.put(new JSONObject().put("role","tool").put("kind","tool_result").put("text","First result"));turn.put("part_end",5);show(h,value);assertSame(activity,response.activity);assertSame(detail,views(activity,NativeToolDetail.class).get(0));assertSame(document,input.getText());assertEquals(6,input.getSelectionEnd());TextView output=text(detail,"First result");
            parts.getJSONObject(4).put("text","First result with live tail");turn.put("model","updated-model");show(h,value);assertSame(activity,response.activity);assertSame(output,text(detail,"First result with live tail"));assertSame(document,input.getText());assertEquals(6,input.getSelectionEnd());
            parts.put(new JSONObject().put("role","assistant").put("kind","text").put("phase","final_answer").put("text","Final answer"));turn.put("part_end",6);show(h,value);assertSame(activity,response.activity);assertTrue(views(activity,NativeToolDetail.class).isEmpty());assertFalse(detail.isAttachedToWindow());assertNotNull(text(response,"Final answer"));assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void changedPromptsAndAttachmentsRetainMountedUserFooterDocumentsAndUnchangedImages() throws Exception {
        try(MainActivityGoalTest.Harness h=new MainActivityGoalTest.Harness(true)) {
            List<Runnable> loads=new ArrayList<>();h.set("fileImages",new NativeImages(loads::add,path->{throw new AssertionError("Do not execute image API in identity test");}));
            JSONObject value=chat(),turn=value.getJSONArray("turns").getJSONObject(0),image=new JSONObject("{id:'a',name:'old.png',media_type:'image/png'}"),file=new JSONObject("{id:'b',name:'note.txt',media_type:'text/plain',excerpt:'Original excerpt'}");
            turn.put("attachments",new JSONArray().put(image).put(file));show(h,value);NativeUserRow row=views(h.get("historyContent"),NativeUserRow.class).get(0);NativeMessage message=row.message;
            message.toggle.performClick();layout(h);TextView selected=text(row,"Long prompt");Object document=selected.getText();Selection.setSelection((Spannable)document,2,8);message.footer.copy.performClick();
            NativeAttachmentCards cards=row.attachments;NativeAttachmentCard first=(NativeAttachmentCard)cards.getChildAt(0),second=(NativeAttachmentCard)cards.getChildAt(1);View imageView=first.image;Object drawable=first.image.getDrawable();assertEquals(1,loads.size());
            value.getJSONObject("transcript").getJSONArray("parts").getJSONObject(0).put("text","Long prompt\n".repeat(16)+"\nUpdated tail");image.put("name","latest.png");file.put("excerpt","Updated excerpt");turn.put("attachments",new JSONArray().put(file).put(image));turn.put("started_at",3000);show(h,value);
            assertSame(row,views(h.get("historyContent"),NativeUserRow.class).get(0));assertSame(cards,row.attachments);assertSame(second,cards.getChildAt(0));assertSame(first,cards.getChildAt(1));assertSame(imageView,first.image);assertSame(drawable,first.image.getDrawable());assertEquals(1,loads.size());
            assertSame(document,selected.getText());assertEquals(8,selected.getSelectionEnd());assertTrue(message.content.expanded);assertEquals("Message copied",message.footer.copy.getContentDescription());assertNotNull(text(second,"Updated excerpt"));
            first.open.performClick();NativeImagePreview preview=h.get("imagePreview");assertNotNull(preview);assertEquals("Preview latest.png",preview.content.getContentDescription());h.call("closeImagePreview");
            turn.put("attachments",new JSONArray());show(h,value);assertNull(row.attachments);first.open.performClick();assertNull(h.get("imagePreview"));assertSame(document,selected.getText());assertEquals(8,selected.getSelectionEnd());
            View bubble=message.bubble;value.getJSONObject("transcript").getJSONArray("parts").getJSONObject(0).put("text","");show(h,value);
            assertNull("No text/files means the entire user row unmounts, as on Web",row.getParent());assertEquals(1,views(h.get("historyContent"),NativeUserRow.class).size());assertEquals(2,views(h.get("historyContent"),NativeTurn.class).size());
            value.getJSONObject("transcript").getJSONArray("parts").getJSONObject(0).put("text","Long prompt\n".repeat(16));show(h,value);
            NativeUserRow restored=views(h.get("historyContent"),NativeUserRow.class).get(0);assertNotSame(row,restored);assertNotSame(bubble,restored.message.bubble);assertFalse(restored.message.content.expanded);assertEquals("Copy message",restored.message.footer.copy.getContentDescription());assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void retiredOrBackgroundAttachmentControlsCannotPreviewAnotherSelectionOrRevokedWorkspace() throws Exception {
        try(MainActivityGoalTest.Harness h=new MainActivityGoalTest.Harness(true)) {
            h.set("fileImages",new NativeImages(task->{},path->{throw new AssertionError("No image API in click-ownership test");}));JSONObject value=chat();value.getJSONArray("turns").getJSONObject(0).put("attachments",new JSONArray("[{id:'a',name:'image.png',media_type:'image/png'}]"));show(h,value);
            NativeUserRow row=views(h.get("historyContent"),NativeUserRow.class).get(0);View open=((NativeAttachmentCard)row.attachments.getChildAt(0)).open;
            h.controller.pause().stop();open.performClick();assertNull(h.get("imagePreview"));h.controller.start().resume();h.set("foreground",true);
            h.set("selectionEpoch",h.<Integer>get("selectionEpoch")+1);open.performClick();assertNull(h.get("imagePreview"));show(h,value);open.performClick();assertNotNull(h.get("imagePreview"));h.call("closeImagePreview");
            h.call("render");open.performClick();assertNull(h.get("imagePreview"));NativeUserRow current=views(h.get("historyContent"),NativeUserRow.class).get(0);View next=((NativeAttachmentCard)current.attachments.getChildAt(0)).open;
            h.set("user",null);next.performClick();assertNull(h.get("imagePreview"));assertTrue(MainActivityGoalTest.ShadowClient.calls.isEmpty());
        }
    }

    @Test public void approvalsStayKeyedAndRetiredButtonsCannotSubmitForAnotherApprovalOrChat() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value=chat(); JSONObject turn=value.getJSONArray("turns").getJSONObject(1);
            turn.put("pending_approval",new JSONObject().put("id","approval-one").put("title","Review action").put("detail","Detail")); show(h,value);
            Button old=textButton(h.get("historyContent"),"Allow"); show(h,new JSONObject(value.toString())); assertSame(old,textButton(h.get("historyContent"),"Allow"));
            turn.put("pending_approval",new JSONObject().put("id","approval-two").put("title","New review")); show(h,value); Button current=textButton(h.get("historyContent"),"Allow");
            old.performClick(); assertTrue(h.io.jobs.isEmpty()); current.performClick(); assertEquals(1,h.io.jobs.size()); h.io.run();
            List<String> call=MainActivityGoalTest.ShadowClient.calls.get(0); assertEquals("/api/chats/chat-one/approvals",call.get(1)); assertEquals("approval-two",new JSONObject(call.get(2)).getString("id"));
            ReflectionHelpers.callInstanceMethod(h.activity,"openChat",ReflectionHelpers.ClassParameter.from(String.class,"other-chat")); current.performClick(); assertTrue(h.io.jobs.isEmpty());
        }
    }
    private Button textButton(View parent,String value) { for(Button button:views(parent,Button.class))if(button.getText().toString().equals(value))return button; throw new AssertionError(value); }

    @Test public void latestIsWiredToTheCurrentActivitySurfaceAndOldOrBackgroundButtonsAreInert() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            show(h,chat()); NativeComposerDock old=h.get("composerDock"); androidx.core.widget.NestedScrollView scroll=h.get("history");
            views(h.get("historyContent"),NativeUserRow.class).get(0).message.toggle.performClick(); layout(h);
            scroll.scrollTo(0,0); assertEquals(View.VISIBLE,old.latest.getVisibility()); old.latest.performClick();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400)); assertEquals(h.<View>get("historyContent").getHeight()-scroll.getHeight(),scroll.getScrollY());
            h.call("render"); layout(h); androidx.core.widget.NestedScrollView next=h.get("history"); next.scrollTo(0,0);
            old.latest.performClick(); Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400)); assertEquals(0,next.getScrollY());
            h.controller.pause().stop(); h.<NativeComposerDock>get("composerDock").latest.performClick();
            Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400)); assertEquals(0,next.getScrollY());
        }
    }

    @Test public void stoppedErrorsAreHiddenAndRetiredTurnViewsAreRemoved() throws Exception {
        try(MainActivityGoalTest.Harness h = new MainActivityGoalTest.Harness(true)) {
            JSONObject value=chat(); value.getJSONArray("turns").getJSONObject(1).put("error","Failure text"); show(h,value); TextView error=text(h.get("historyContent"),"Failure text");
            value.getJSONArray("turns").getJSONObject(1).put("status","stopped"); show(h,value); assertNull(error.getParent());
            NativeUserRow retired=views(h.get("historyContent"),NativeUserRow.class).get(0); value.getJSONArray("turns").remove(0); show(h,value); assertNull(retired.getParent());
            assertEquals(1,views(h.get("historyContent"),NativeTurn.class).size());
        }
    }
}
