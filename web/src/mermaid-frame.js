// Classic script shared by the sandboxed Web iframe and offline Android document.
function mermaidFrame() {
  const scope = window;
  const diagram = document.getElementById("diagram");
  const scratch = document.getElementById("scratch");
  let latest = 0;
  let rendered = 0;
  let sequence = 0;
  let queue = Promise.resolve();
  let expanded = false;
  let zoom = 1,
    width = 1,
    height = 1;
  const resize = (next) => {
    const svg = diagram.querySelector("svg");
    if (!svg || !expanded) return;
    const previous = zoom;
    zoom = Math.max(0.1, Math.min(8, next));
    const x = (diagram.scrollLeft + diagram.clientWidth / 2) / previous;
    const y = (diagram.scrollTop + diagram.clientHeight / 2) / previous;
    svg.style.maxWidth = "none";
    svg.style.width = `${width * zoom}px`;
    svg.style.height = `${height * zoom}px`;
    diagram.scrollLeft = x * zoom - diagram.clientWidth / 2;
    diagram.scrollTop = y * zoom - diagram.clientHeight / 2;
  };
  const fit = () => {
    resize(
      Math.min(
        (diagram.clientWidth - 32) / width,
        (diagram.clientHeight - 28) / height,
      ),
    );
    diagram.scrollLeft = diagram.scrollTop = 0;
  };
  scope.spoolZoomMermaid = (action) => {
    if (action === "fit") fit();
    else resize(zoom * (action === "in" ? 1.25 : 1 / 1.25));
  };
  const pointers = new Map();
  diagram.addEventListener("pointerdown", (event) => {
    if (!expanded) return;
    pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
    diagram.setPointerCapture(event.pointerId);
  });
  diagram.addEventListener("pointermove", (event) => {
    const previous = pointers.get(event.pointerId);
    if (!expanded || !previous) return;
    const other = [...pointers.entries()].find(
      ([id]) => id !== event.pointerId,
    )?.[1];
    if (other) {
      const before = Math.hypot(previous.x - other.x, previous.y - other.y);
      const after = Math.hypot(
        event.clientX - other.x,
        event.clientY - other.y,
      );
      if (before > 0) resize((zoom * after) / before);
    } else {
      diagram.scrollLeft -= event.clientX - previous.x;
      diagram.scrollTop -= event.clientY - previous.y;
    }
    pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
  });
  for (const name of ["pointerup", "pointercancel", "lostpointercapture"])
    diagram.addEventListener(name, (event) => pointers.delete(event.pointerId));
  diagram.addEventListener(
    "wheel",
    (event) => {
      if (!expanded || !(event.ctrlKey || event.metaKey)) return;
      event.preventDefault();
      resize(zoom * Math.exp(-event.deltaY * 0.01));
    },
    { passive: false },
  );
  const notify = (generation, ready) => {
    const height = ready
      ? Math.ceil(diagram.getBoundingClientRect().height)
      : 0;
    const message = { type: "spool:mermaid", generation, ready, height };
    if (scope.SpoolMermaid)
      scope.SpoolMermaid.rendered(generation, ready, height);
    else window.parent.postMessage(message, "*");
  };
  scope.spoolRenderMermaid = (source, dark, generation, full = false) => {
    latest = generation;
    queue = queue.then(async () => {
      if (generation !== latest) return;
      rendered = 0;
      diagram.replaceChildren();
      document.documentElement.style.colorScheme = dark ? "dark" : "light";
      try {
        scope.mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          // Keep these settings protected from diagram frontmatter/directives.
          secure: [
            "secure",
            "securityLevel",
            "startOnLoad",
            "maxTextSize",
            "maxEdges",
            "htmlLabels",
            "suppressErrorRendering",
          ],
          htmlLabels: false,
          suppressErrorRendering: true,
          theme: dark ? "dark" : "default",
          fontFamily: "system-ui, sans-serif",
        });
        const { svg } = await scope.mermaid.render(
          `spool-mermaid-${++sequence}`,
          source,
          scratch,
        );
        if (generation !== latest) return;
        diagram.innerHTML = svg;
        // Strict mode retains SVG links. Remove their destinations before the
        // diagram becomes interactive, including legacy xlink:href attributes.
        for (const anchor of diagram.querySelectorAll("a")) {
          anchor.removeAttribute("href");
          anchor.removeAttributeNS("http://www.w3.org/1999/xlink", "href");
          anchor.removeAttribute("target");
        }
        expanded = full;
        diagram.style.height = full ? "100vh" : "";
        diagram.style.boxSizing = "border-box";
        diagram.style.touchAction = full ? "none" : "";
        diagram.style.cursor = full ? "grab" : "";
        if (full) {
          const bounds = diagram.querySelector("svg").viewBox.baseVal;
          width =
            bounds.width ||
            diagram.querySelector("svg").getBoundingClientRect().width;
          height =
            bounds.height ||
            diagram.querySelector("svg").getBoundingClientRect().height;
          zoom = 1;
          fit();
        }
        // Leave diagram callback handlers unbound as well.
        rendered = generation;
        notify(generation, true);
      } catch {
        if (generation === latest) notify(generation, false);
      } finally {
        scratch.replaceChildren();
      }
    });
  };
  window.addEventListener("message", (event) => {
    if (event.source !== window.parent || !event.data) return;
    if (
      event.data.type === "spool:zoom-mermaid" &&
      ["in", "out", "fit"].includes(event.data.action)
    ) {
      scope.spoolZoomMermaid(event.data.action);
      return;
    }
    if (event.data.type !== "spool:render-mermaid") return;
    const { source, dark, generation, expanded } = event.data;
    if (
      typeof source === "string" &&
      typeof dark === "boolean" &&
      Number.isSafeInteger(generation)
    )
      scope.spoolRenderMermaid(source, dark, generation, expanded === true);
  });
  new ResizeObserver(() => {
    if (rendered && rendered === latest) notify(rendered, true);
  }).observe(diagram);
}

mermaidFrame();
