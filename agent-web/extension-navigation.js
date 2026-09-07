// Knowledge-owned bridge for the current Agent Native Web shell.
// It is copied into the dedicated dist at build time; the Agent checkout is
// never patched. The bridge uses the public extension inventory API and the
// stable native sidebar slot anchor exposed by the Agent UI contract.
(() => {
  const navigationId = "shutu-knowledge-extension-navigation";
  const hostId = `${navigationId}-host`;
  const apiPath = "/api/extensions";
  const retryMs = 1500;

  function extensionPrefix() {
    const match = window.location.pathname.match(/^(.*\/extensions\/[^/]+)/);
    return match ? match[1] : "";
  }

  function endpoint(path) {
    return `${extensionPrefix()}${path}`;
  }

  function styleHost(host) {
    host.style.flex = "none";
    host.style.display = "flex";
    host.style.flexDirection = "column";
    host.style.gap = "4px";
    host.style.margin = "0 4px 8px";
    host.style.padding = "0 4px";
    host.style.color = "var(--dsw-alias-label-secondary, #666)";
    host.style.fontSize = "13px";
  }

  function styleHeading(heading) {
    heading.style.padding = "0 8px";
    heading.style.fontSize = "11px";
    heading.style.fontWeight = "600";
    heading.style.lineHeight = "20px";
    heading.style.letterSpacing = "0.04em";
    heading.style.textTransform = "uppercase";
    heading.style.color = "var(--dsw-alias-label-tertiary, #888)";
  }

  function styleLink(link) {
    link.style.display = "flex";
    link.style.alignItems = "center";
    link.style.minHeight = "32px";
    link.style.boxSizing = "border-box";
    link.style.padding = "0 8px";
    link.style.borderRadius = "8px";
    link.style.color = "var(--dsw-alias-label-primary, #222)";
    link.style.textDecoration = "none";
    link.style.fontSize = "13px";
    link.style.fontWeight = "500";
    link.addEventListener("mouseenter", () => {
      link.style.background = "var(--dsw-alias-interactive-bg-hover, rgba(0,0,0,.06))";
    });
    link.addEventListener("mouseleave", () => { link.style.background = "transparent"; });
  }

  function syncCollapsed(host, anchor) {
    const width = anchor.getBoundingClientRect().width;
    const compact = width > 0 && width < 120;
    host.style.margin = compact ? "0 0 8px" : "0 4px 8px";
    host.style.padding = compact ? "0" : "0 4px";
    const heading = host.querySelector(`[data-${navigationId}-heading]`);
    const link = host.querySelector(`[data-${navigationId}-link]`);
    if (heading instanceof HTMLElement) heading.hidden = compact;
    if (link instanceof HTMLElement) {
      link.title = compact ? link.dataset.fullLabel || link.textContent || "Knowledge" : "";
      link.style.justifyContent = compact ? "center" : "flex-start";
      link.style.padding = compact ? "0" : "0 8px";
      link.style.width = compact ? "36px" : "auto";
      link.style.overflow = "hidden";
      link.style.whiteSpace = "nowrap";
    }
  }

  function render(anchor, extension) {
    let host = anchor.querySelector(`#${hostId}`);
    if (!(host instanceof HTMLElement)) {
      host = document.createElement("div");
      host.id = hostId;
      host.setAttribute(`data-${navigationId}`, "true");
      styleHost(host);
      anchor.prepend(host);
    }

    host.replaceChildren();
    const heading = document.createElement("div");
    heading.textContent = "Tools";
    heading.setAttribute(`data-${navigationId}-heading`, "true");
    styleHeading(heading);
    const link = document.createElement("a");
    link.href = extension.route || "/extensions/shutu-knowledge/";
    link.textContent = extension.title || "Knowledge";
    link.dataset.fullLabel = link.textContent;
    link.setAttribute(`data-${navigationId}-link`, "true");
    link.addEventListener("click", () => {
      // Agent's Native Web keeps the active locale on <html>. The two apps
      // have separate bundles, so carry that explicit choice across the
      // same-origin extension handoff without touching Agent source.
      const language = document.documentElement.lang.toLowerCase().startsWith("zh") ? "zh" : "en";
      localStorage.setItem("knowledge-language", language);
    });
    styleLink(link);
    host.append(heading, link);
    syncCollapsed(host, anchor);

    if (host.__resizeObserver) host.__resizeObserver.disconnect();
    if (typeof ResizeObserver === "function") {
      host.__resizeObserver = new ResizeObserver(() => syncCollapsed(host, anchor));
      host.__resizeObserver.observe(anchor);
    }
  }

  async function load() {
    try {
      const response = await fetch(endpoint(apiPath), { headers: { accept: "application/json" } });
      if (!response.ok) throw new Error(`extension inventory HTTP ${response.status}`);
      const payload = await response.json();
      const extension = (payload.extensions || []).find((item) =>
        item.extensionId === "shutu-knowledge" && item.navigationEnabled && item.ready !== false,
      );
      const anchor = document.querySelector('[data-slot="sidebar.workspaces"]');
      if (anchor instanceof HTMLElement && extension) render(anchor, extension);
    } catch (error) {
      console.debug("Knowledge extension navigation is waiting for Agent", error);
    }
    window.setTimeout(load, retryMs);
  }

  load();
})();
