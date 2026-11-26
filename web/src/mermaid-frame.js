// Classic script shared by the sandboxed Web iframe and offline Android document.
function mermaidFrame() {
  const scope = window;
  const diagram = document.getElementById("diagram");
  const scratch = document.getElementById("scratch");
  let latest = 0;
  let rendered = 0;
  let sequence = 0;
  let exported = 0;
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
  const snapshot = (native) => {
    // Resolve styles before releasing the compiler. The result can be painted
    // as a static SVG image on Web/Desktop or a native picture on Android.
    const original = diagram.querySelector("svg");
    const copy = original.cloneNode(true);
    const properties = [
      "fill",
      "fill-opacity",
      "fill-rule",
      "stroke",
      "stroke-opacity",
      "stroke-width",
      "stroke-linecap",
      "stroke-linejoin",
      "stroke-miterlimit",
      "stroke-dasharray",
      "stroke-dashoffset",
      "opacity",
      "color",
      "font-family",
      "font-size",
      "font-weight",
      "font-style",
      "font-variant",
      "font-stretch",
      "letter-spacing",
      "word-spacing",
      "text-anchor",
      "text-decoration",
      "dominant-baseline",
      "alignment-baseline",
      "baseline-shift",
      "direction",
      "unicode-bidi",
      "writing-mode",
      "white-space",
      "visibility",
      "display",
      "overflow",
      "clip-path",
      "clip-rule",
      "mask",
      "filter",
      "stop-color",
      "stop-opacity",
      "flood-color",
      "flood-opacity",
      "vector-effect",
      "paint-order",
      "marker-start",
      "marker-mid",
      "marker-end",
    ];
    const setTransform = (node, matrix) =>
      node.setAttribute(
        "transform",
        `matrix(${matrix.a}, ${matrix.b}, ${matrix.c}, ${matrix.d}, ${matrix.e}, ${matrix.f})`,
      );
    const geometry = {
      rect: ["x", "y", "width", "height", "rx", "ry"],
      circle: ["cx", "cy", "r"],
      ellipse: ["cx", "cy", "rx", "ry"],
      svg: ["x", "y", "width", "height"],
      use: ["x", "y", "width", "height"],
    };
    const originals = [original, ...original.querySelectorAll("*")];
    const copies = [copy, ...copy.querySelectorAll("*")];
    originals.forEach((node, index) => {
      const style = getComputedStyle(node);
      copies[index].removeAttribute("style");
      for (const property of properties) {
        let value = style.getPropertyValue(property);
        // Chromium can make fragment paint references absolute.
        value = value.replace(
          /url\(["']?[^)#]*#([^)'" ]+)["']?\)/g,
          "url(#$1)",
        );
        if (value) copies[index].setAttribute(property, value);
      }
      // SVG 2 geometry can come from CSS, including classDef corner radii.
      // Materialize lengths as SVG attributes understood by AndroidSVG too.
      for (const property of geometry[node.localName] || []) {
        const value = style.getPropertyValue(property);
        if (value && value !== "auto")
          copies[index].setAttribute(property, value);
      }
      const transformed =
        node.hasAttribute("transform") || style.transform !== "none";
      const matrix = transformed ? node.getCTM?.() : null;
      const parentMatrix = transformed ? node.parentElement?.getCTM?.() : null;
      if (node.localName !== "svg" && matrix && parentMatrix) {
        // CTMs include CSS pivots and SVG transforms, which WebKit omits from
        // getComputedStyle(). Remove only the inherited coordinate system.
        setTransform(copies[index], parentMatrix.inverse().multiply(matrix));
      } else if (style.transform !== "none") {
        let [x, y] = style.transformOrigin.split(" ").map(parseFloat);
        if (
          style.transformBox === "fill-box" ||
          style.transformBox === "stroke-box"
        ) {
          const bounds = node.getBBox();
          x += bounds.x;
          y += bounds.y;
        }
        // SVG 1.1 has no CSS transform-origin. Bake its pivot into the matrix.
        setTransform(
          copies[index],
          new DOMMatrix()
            .translate(x, y)
            .multiply(new DOMMatrix(style.transform))
            .translate(-x, -y),
        );
      }
      copies[index].removeAttribute("transform-origin");
      copies[index].removeAttribute("transform-box");
    });
    if (native)
      originals.forEach((node, index) => {
        if (node.localName === "text") {
          // AndroidSVG does not implement text baselines. Measure the same label
          // on the alphabetic baseline and preserve its position with a translation.
          const probe = node.cloneNode(true);
          for (const text of [probe, ...probe.querySelectorAll("tspan")]) {
            text.setAttribute("dominant-baseline", "auto");
            text.setAttribute("alignment-baseline", "auto");
            text.setAttribute("baseline-shift", "0");
            text.style.setProperty("dominant-baseline", "auto", "important");
            text.style.setProperty("alignment-baseline", "auto", "important");
            text.style.setProperty("baseline-shift", "0", "important");
          }
          const before = node.getBBox();
          let offset;
          try {
            node.parentNode.appendChild(probe);
            offset = before.y - probe.getBBox().y;
          } finally {
            probe.remove();
          }
          for (const text of [
            copies[index],
            ...copies[index].querySelectorAll("tspan"),
          ]) {
            text.setAttribute("dominant-baseline", "auto");
            text.setAttribute("alignment-baseline", "auto");
            text.setAttribute("baseline-shift", "0");
          }
          if (offset)
            setTransform(
              copies[index],
              new DOMMatrix(
                copies[index].getAttribute("transform") || undefined,
              ).translate(0, offset),
            );
        }
      });
    for (const node of copy.querySelectorAll("style,script")) node.remove();
    // AndroidSVG supports SVG 1.1 markers. Preserve SVG 2's reversed start
    // arrow by giving start references their own reflected marker geometry.
    for (const marker of native
      ? copy.querySelectorAll('marker[orient="auto-start-reverse"]')
      : []) {
      marker.setAttribute("orient", "auto");
      const starts = [...copy.querySelectorAll("[marker-start]")].filter(
        (node) => node.getAttribute("marker-start") === `url(#${marker.id})`,
      );
      if (!starts.length) continue;
      const reversed = marker.cloneNode(true);
      reversed.id += "-native-start";
      const transform = document.createElementNS(
        "http://www.w3.org/2000/svg",
        "g",
      );
      transform.setAttribute(
        "transform",
        `rotate(180 ${marker.refX.baseVal.value} ${marker.refY.baseVal.value})`,
      );
      while (reversed.firstChild) transform.appendChild(reversed.firstChild);
      reversed.appendChild(transform);
      marker.parentNode.appendChild(reversed);
      for (const start of starts)
        start.setAttribute("marker-start", `url(#${reversed.id})`);
    }
    const bounds = original.viewBox.baseVal;
    copy.setAttribute("width", String(bounds.width));
    copy.setAttribute("height", String(bounds.height));

    const svg = new XMLSerializer().serializeToString(copy);
    if (svg.length > 1024 * 1024)
      throw new Error("Diagram output is too large");
    return svg;
  };
  let exportWeb = false;
  const notify = (generation, ready) => {
    const height = ready
      ? Math.ceil(diagram.getBoundingClientRect().height)
      : 0;
    const message = { type: "spool:mermaid", generation, ready, height };
    if (scope.SpoolMermaid) {
      scope.SpoolMermaid.rendered(generation, ready, height);
      if (ready && exported !== generation && scope.SpoolMermaid.snapshot) {
        exported = generation;
        scope.SpoolMermaid.snapshot(generation, snapshot(true));
      }
    } else {
      if (ready && exportWeb && exported !== generation) {
        exported = generation;
        message.svg = snapshot(false);
      }
      window.parent.postMessage(message, "*");
    }
  };
  scope.spoolRenderMermaid = (
    source,
    dark,
    generation,
    full = false,
    output = false,
  ) => {
    latest = generation;
    queue = queue.then(async () => {
      if (generation !== latest) return;
      rendered = 0;
      exportWeb = output;
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
    const { source, dark, generation, expanded, snapshot } = event.data;
    if (
      typeof source === "string" &&
      typeof dark === "boolean" &&
      Number.isSafeInteger(generation)
    )
      scope.spoolRenderMermaid(
        source,
        dark,
        generation,
        expanded === true,
        snapshot === true,
      );
  });
  new ResizeObserver(() => {
    if (rendered && rendered === latest) notify(rendered, true);
  }).observe(diagram);
}

mermaidFrame();
