import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ComposerSendButton } from "../.test-build/components/web/src/composer-send-button.js";

const render = (props) => renderToStaticMarkup(createElement(ComposerSendButton, {
  active: false, stopping: false, disabled: false, t: (key) => key, onStop: () => {}, ...props,
}));

test("shared send action preserves submit, exact glyph sizes and disabled/busy semantics", () => {
  const send = render({});
  assert.match(send, /type="submit"/); assert.match(send, /aria-label="Send message"/);
  assert.match(send, /width="20"/); assert.doesNotMatch(send, /aria-busy|disabled/);
  const stop = render({ active: true });
  assert.match(stop, /type="button"/); assert.match(stop, /aria-label="Stop response"/);
  assert.match(stop, /width="14"/); assert.match(stop, /fill="currentColor"/);
  const stopping = render({ active: true, stopping: true, disabled: true });
  assert.match(stopping, /aria-label="Stopping…"/); assert.match(stopping, /aria-busy="true"/);
  assert.match(stopping, /disabled=""/); assert.match(stopping, /width="18"/); assert.match(stopping, /stop-spinner/);
});
