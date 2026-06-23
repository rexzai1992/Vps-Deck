"use strict";

const formatBytes = (value) => {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
  let size = bytes;
  let index = -1;
  do {
    size /= 1024;
    index += 1;
  } while (size >= 1024 && index < units.length - 1);
  return `${size.toFixed(1)} ${units[index]}`;
};

const formatTime = (value) => {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleTimeString();
};

const setStatus = (element, status) => {
  if (!element) return;
  element.className = `status status-${status}`;
  element.replaceChildren();
  const dot = document.createElement("i");
  element.append(dot, document.createTextNode(status));
};

const fetchJSON = async (path) => {
  const response = await fetch(path, { headers: { Accept: "application/json" } });
  if (response.status === 401) {
    window.location.assign("/login?error=Please+sign+in+to+continue.");
    throw new Error("Authentication required");
  }
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data.error || "Request failed");
  }
  return data;
};

document.addEventListener("submit", (event) => {
  const message = event.target.dataset.confirm;
  if (message && !window.confirm(message)) {
    event.preventDefault();
  }
});

document.addEventListener("click", (event) => {
  const addButton = event.target.closest("[data-add-env]");
  if (addButton) {
    const template = document.querySelector("[data-env-template]");
    const list = document.querySelector("[data-env-list]");
    if (template && list) {
      list.append(template.content.cloneNode(true));
      list.lastElementChild.querySelector("input").focus();
    }
    return;
  }

  const removeButton = event.target.closest("[data-remove-env]");
  if (removeButton) {
    removeButton.closest(".env-row").remove();
    return;
  }

  const toggleButton = event.target.closest("[data-toggle-secret]");
  if (toggleButton) {
    const input = toggleButton.parentElement.querySelector("input");
    const showing = input.type === "text";
    input.type = showing ? "password" : "text";
    toggleButton.textContent = showing ? "Show" : "Hide";
    return;
  }

  const copyButton = event.target.closest("[data-copy-target]");
  if (copyButton) {
    const source = document.getElementById(copyButton.dataset.copyTarget);
    if (!source) return;
    const copy = navigator.clipboard?.writeText
      ? navigator.clipboard.writeText(source.textContent)
      : new Promise((resolve, reject) => {
          const temporary = document.createElement("textarea");
          temporary.value = source.textContent;
          temporary.style.position = "fixed";
          temporary.style.opacity = "0";
          document.body.append(temporary);
          temporary.select();
          const copied = document.execCommand("copy");
          temporary.remove();
          copied ? resolve() : reject(new Error("Copy failed"));
        });
    copy.then(() => {
      const original = copyButton.textContent;
      copyButton.textContent = "Copied";
      window.setTimeout(() => {
        copyButton.textContent = original;
      }, 1500);
    }).catch(() => {
      copyButton.textContent = "Select and copy manually";
    });
  }
});

const setupFolderPicker = () => {
  const dialog = document.querySelector("[data-folder-dialog]");
  const openButton = document.querySelector("[data-folder-open]");
  if (!dialog || !openButton) return;

  const rootSelect = dialog.querySelector("[data-folder-root]");
  const hiddenToggle = dialog.querySelector("[data-folder-hidden]");
  const breadcrumbs = dialog.querySelector("[data-folder-breadcrumbs]");
  const folderList = dialog.querySelector("[data-folder-list]");
  const current = dialog.querySelector("[data-folder-current]");
  const warning = dialog.querySelector("[data-folder-warning]");
  const selectButton = dialog.querySelector("[data-folder-select]");
  const destination = document.querySelector("[data-folder-value]");
  let listing = null;

  const load = async (path = "") => {
    folderList.replaceChildren();
    const loading = document.createElement("div");
    loading.className = "folder-loading";
    loading.textContent = "Loading folders…";
    folderList.append(loading);
    selectButton.disabled = true;
    const query = new URLSearchParams({
      root: rootSelect.value,
      path,
      hidden: String(hiddenToggle.checked),
    });
    try {
      listing = await fetchJSON(`/api/folders?${query}`);
      current.textContent = listing.absolute_path;
      warning.hidden = !listing.warning;
      warning.textContent = listing.warning || "";
      breadcrumbs.replaceChildren();
      listing.breadcrumbs.forEach((crumb, index) => {
        if (index > 0) {
          const separator = document.createElement("span");
          separator.textContent = "/";
          breadcrumbs.append(separator);
        }
        const button = document.createElement("button");
        button.type = "button";
        button.textContent = crumb.name;
        button.addEventListener("click", () => load(crumb.relative_path));
        breadcrumbs.append(button);
      });

      folderList.replaceChildren();
      if (listing.current_path && listing.current_path !== ".") {
        const parent = document.createElement("button");
        parent.type = "button";
        parent.className = "folder-choice parent";
        parent.append(document.createTextNode("↰  Parent folder"));
        parent.addEventListener("click", () => load(listing.parent_path));
        folderList.append(parent);
      }
      listing.folders.forEach((folder) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "folder-choice";
        const icon = document.createElement("span");
        icon.textContent = "◆";
        const label = document.createElement("span");
        label.textContent = folder.name;
        button.append(icon, label);
        button.addEventListener("click", () => load(folder.relative_path));
        folderList.append(button);
      });
      if (listing.folders.length === 0 && (!listing.current_path || listing.current_path === ".")) {
        const empty = document.createElement("div");
        empty.className = "folder-loading";
        empty.textContent = "No child folders. You can select this location.";
        folderList.append(empty);
      }
      selectButton.disabled = false;
    } catch (error) {
      listing = null;
      folderList.replaceChildren();
      const message = document.createElement("div");
      message.className = "alert alert-error";
      message.textContent = error.message;
      folderList.append(message);
      warning.hidden = true;
    }
  };

  openButton.addEventListener("click", () => {
    dialog.showModal();
    load("");
  });
  dialog.querySelectorAll("[data-folder-close]").forEach((button) => {
    button.addEventListener("click", () => dialog.close());
  });
  rootSelect.addEventListener("change", () => load(""));
  hiddenToggle.addEventListener("change", () => load(listing ? listing.current_path : ""));
  selectButton.addEventListener("click", () => {
    if (!listing) return;
    destination.value = listing.absolute_path;
    destination.dispatchEvent(new Event("change", { bubbles: true }));
    dialog.close();
  });
};

const applyPortFilters = () => {
  const container = document.querySelector("[data-monitor-ports]");
  if (!container) return;
  const search = container.querySelector("[data-port-search]").value.trim().toLowerCase();
  const protocol = container.querySelector("[data-port-protocol]").value;
  const exposure = container.querySelector("[data-port-exposure]").value;
  container.querySelectorAll("[data-port-row]").forEach((row) => {
    const matchesSearch = !search || row.dataset.search.toLowerCase().includes(search);
    const matchesProtocol = !protocol || row.dataset.protocol === protocol;
    const matchesExposure = !exposure || row.dataset.exposure === exposure;
    row.hidden = !(matchesSearch && matchesProtocol && matchesExposure);
  });
};

const renderPorts = (data) => {
  const container = document.querySelector("[data-monitor-ports]");
  if (!container) return;
  setStatus(container.querySelector("[data-ports-status]"), data.status);
  container.querySelector("[data-ports-total]").textContent = data.total_count;
  container.querySelector("[data-ports-tcp]").textContent = data.tcp_count;
  container.querySelector("[data-ports-udp]").textContent = data.udp_count;
  container.querySelector("[data-ports-missing]").textContent = data.missing_project_ports.length;
  container.querySelector("[data-ports-updated]").textContent = formatTime(data.collected_at);
  const warning = container.querySelector("[data-ports-warning]");
  warning.hidden = !data.warning;
  warning.textContent = data.warning || "";

  const rows = container.querySelector("[data-port-rows]");
  rows.replaceChildren();
  data.listeners.forEach((listener) => {
    const row = document.createElement("tr");
    row.dataset.portRow = "";
    row.dataset.protocol = listener.protocol;
    row.dataset.exposure = listener.exposure_key;
    row.dataset.search = [
      listener.address,
      listener.port,
      listener.process,
      ...(listener.projects || []).map((project) => project.name),
    ].join(" ");

    const protocolCell = document.createElement("td");
    const protocol = document.createElement("span");
    protocol.className = "protocol-badge";
    protocol.textContent = listener.protocol;
    protocolCell.append(protocol);

    const addressCell = document.createElement("td");
    const address = document.createElement("code");
    address.textContent = listener.address;
    addressCell.append(address);

    const portCell = document.createElement("td");
    const port = document.createElement("strong");
    port.textContent = listener.port;
    portCell.append(port);

    const exposureCell = document.createElement("td");
    exposureCell.textContent = listener.exposure;

    const processCell = document.createElement("td");
    processCell.textContent = listener.process || "Unknown";
    if (listener.pid) {
      const pid = document.createElement("small");
      pid.textContent = `PID ${listener.pid}`;
      processCell.append(pid);
    }

    const projectCell = document.createElement("td");
    if ((listener.projects || []).length === 0) {
      projectCell.textContent = "—";
    } else {
      listener.projects.forEach((project, index) => {
        if (index > 0) projectCell.append(document.createTextNode(", "));
        const link = document.createElement("a");
        link.href = `/projects/${project.id}`;
        link.textContent = project.name;
        projectCell.append(link);
      });
    }
    row.append(protocolCell, addressCell, portCell, exposureCell, processCell, projectCell);
    rows.append(row);
  });
  if (data.listeners.length === 0) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 6;
    cell.className = "empty-cell";
    cell.textContent = "No sockets are visible.";
    row.append(cell);
    rows.append(row);
  }

  const missing = container.querySelector("[data-missing-port-list]");
  missing.replaceChildren();
  data.missing_project_ports.forEach((project) => {
    const link = document.createElement("a");
    link.className = "missing-port";
    link.href = `/projects/${project.id}`;
    const name = document.createElement("strong");
    name.textContent = project.name;
    const portValue = document.createElement("span");
    portValue.textContent = `Port ${project.port}`;
    link.append(name, portValue);
    missing.append(link);
  });
  if (data.missing_project_ports.length === 0) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No missing project ports.";
    missing.append(empty);
  }
  applyPortFilters();
};

const modelCard = (model, running) => {
  const card = document.createElement("article");
  card.className = "model-card";
  const title = document.createElement("h3");
  title.textContent = model.name;
  const description = document.createElement("p");
  description.textContent = [model.details?.parameter_size, model.details?.quantization_level].filter(Boolean).join(" · ") || "Model details unavailable";
  const details = document.createElement("dl");
  const values = running
    ? [["VRAM", formatBytes(model.size_vram)], ["Context", model.context_length || "—"], ["Expires", formatTime(model.expires_at)]]
    : [["Size", formatBytes(model.size)], ["Family", model.details?.family || "—"]];
  values.forEach(([label, value]) => {
    const row = document.createElement("div");
    const term = document.createElement("dt");
    term.textContent = label;
    const definition = document.createElement("dd");
    definition.textContent = value;
    row.append(term, definition);
    details.append(row);
  });
  card.append(title, description, details);
  return card;
};

const renderOllama = (data) => {
  const container = document.querySelector("[data-monitor-ollama]");
  if (!container) return;
  setStatus(container.querySelector("[data-ollama-status]"), data.status);
  container.querySelector("[data-ollama-endpoint]").textContent = data.base_url;
  container.querySelector("[data-ollama-message]").textContent = data.message;
  container.querySelector("[data-ollama-version]").textContent = data.version || "—";
  container.querySelector("[data-ollama-installed]").textContent = data.installed_count;
  container.querySelector("[data-ollama-running]").textContent = data.running_count;
  container.querySelector("[data-ollama-vram]").textContent = formatBytes(data.total_vram);
  container.querySelector("[data-ollama-updated]").textContent = formatTime(data.collected_at);

  const running = container.querySelector("[data-ollama-running-list]");
  running.replaceChildren();
  (data.running_models || []).forEach((model) => running.append(modelCard(model, true)));
  if (!data.running_models?.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No models are loaded.";
    running.append(empty);
  }
  const installed = container.querySelector("[data-ollama-installed-list]");
  installed.replaceChildren();
  (data.installed_models || []).forEach((model) => installed.append(modelCard(model, false)));
  if (!data.installed_models?.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No installed models reported.";
    installed.append(empty);
  }
  container.querySelector("[data-ollama-install]").hidden = data.status === "online" || data.status === "disabled";
};

const updateDashboardMonitors = async () => {
  const container = document.querySelector("[data-monitor-dashboard]");
  if (!container) return;
  const [ports, ollama] = await Promise.allSettled([
    fetchJSON("/api/monitors/ports"),
    fetchJSON("/api/monitors/ollama"),
  ]);
  if (ports.status === "fulfilled") {
    const data = ports.value;
    setStatus(container.querySelector("[data-dashboard-ports-status]"), data.status);
    container.querySelector("[data-dashboard-ports-total]").textContent = data.total_count;
    container.querySelector("[data-dashboard-ports-tcp]").textContent = data.tcp_count;
    container.querySelector("[data-dashboard-ports-udp]").textContent = data.udp_count;
    container.querySelector("[data-dashboard-ports-missing]").textContent = data.missing_project_ports.length;
  } else {
    setStatus(container.querySelector("[data-dashboard-ports-status]"), "degraded");
  }
  if (ollama.status === "fulfilled") {
    const data = ollama.value;
    setStatus(container.querySelector("[data-dashboard-ollama-status]"), data.status);
    container.querySelector("[data-dashboard-ollama-version]").textContent = data.version ? `v${data.version}` : "Unavailable";
    container.querySelector("[data-dashboard-ollama-installed]").textContent = data.installed_count;
    container.querySelector("[data-dashboard-ollama-running]").textContent = data.running_count;
  } else {
    setStatus(container.querySelector("[data-dashboard-ollama-status]"), "degraded");
  }
};

const setupMonitorPolling = () => {
  const ports = document.querySelector("[data-monitor-ports]");
  if (ports) {
    ports.querySelectorAll("[data-port-search], [data-port-protocol], [data-port-exposure]").forEach((control) => {
      control.addEventListener("input", applyPortFilters);
      control.addEventListener("change", applyPortFilters);
    });
    const refresh = Number(ports.dataset.refresh || 5) * 1000;
    window.setInterval(() => fetchJSON("/api/monitors/ports").then(renderPorts).catch(() => setStatus(ports.querySelector("[data-ports-status]"), "degraded")), refresh);
  }
  const ollama = document.querySelector("[data-monitor-ollama]");
  if (ollama) {
    const refresh = Number(ollama.dataset.refresh || 5) * 1000;
    window.setInterval(() => fetchJSON("/api/monitors/ollama").then(renderOllama).catch(() => setStatus(ollama.querySelector("[data-ollama-status]"), "degraded")), refresh);
  }
  const dashboard = document.querySelector("[data-monitor-dashboard]");
  if (dashboard) {
    const refresh = Number(dashboard.dataset.refresh || 5) * 1000;
    window.setInterval(() => updateDashboardMonitors().catch(() => {}), refresh);
  }
};

let toastHost = null;
const showToast = (message, isError) => {
  if (!toastHost) {
    toastHost = document.createElement("div");
    toastHost.className = "toast-host";
    document.body.append(toastHost);
  }
  const toast = document.createElement("div");
  toast.className = isError ? "toast toast-error" : "toast";
  toast.textContent = message;
  toastHost.append(toast);
  window.setTimeout(() => toast.classList.add("toast-hide"), 3200);
  window.setTimeout(() => toast.remove(), 3700);
};

const setupFileExplorer = () => {
  const root = document.querySelector("[data-explorer]");
  if (!root) return;

  const projectID = root.dataset.projectId;
  const csrf = root.dataset.csrf;
  const MOVE_TYPE = "application/x-vpsdeck-move";
  const base = root.dataset.filesBase || `/projects/${projectID}/files`;
  const apiBase = root.dataset.filesApi || `/api/projects/${projectID}/files`;

  const state = {
    path: root.dataset.currentPath === "." ? "" : root.dataset.currentPath,
    listing: null,
    entries: [],
    selection: new Set(),
    clipboard: null,
    lastIndex: null,
    view: window.localStorage.getItem("vpsdeck-file-view") || "details",
  };

  const post = async (url, fields) => {
    const body = new FormData();
    Object.entries(fields).forEach(([key, value]) => {
      if (Array.isArray(value)) value.forEach((item) => body.append(key, item));
      else body.append(key, value);
    });
    const response = await fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": csrf, Accept: "application/json" },
      body,
    });
    if (response.status === 401) {
      window.location.assign("/login?error=Please+sign+in+to+continue.");
      throw new Error("Authentication required");
    }
    let data = {};
    try { data = await response.json(); } catch (_) { /* ignore */ }
    if (!response.ok && !data.ok) throw new Error(data.error || "Action failed");
    return data;
  };

  const childPath = (name) => (state.path ? `${state.path}/${name}` : name);
  const entryByPath = (path) => state.entries.find((entry) => entry.path === path);

  // --- rendering -----------------------------------------------------------
  const ui = {};
  const buildShell = () => {
    root.replaceChildren();

    const toolbar = document.createElement("div");
    toolbar.className = "explorer-toolbar panel";
    const makeButton = (label, key, opts = {}) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = `button button-small${opts.danger ? " button-danger" : ""}`;
      button.textContent = label;
      button.dataset.action = key;
      toolbar.append(button);
      return button;
    };
    const left = document.createElement("div");
    left.className = "explorer-tools";
    const right = document.createElement("div");
    right.className = "explorer-tools";
    toolbar.append(left, right);

    ui.up = makeButton("↰ Up", "up");
    ui.newFolder = makeButton("New folder", "new-folder");
    ui.newFile = makeButton("New file", "new-file");
    ui.upload = makeButton("Upload", "upload");
    ui.download = makeButton("Download", "download");
    ui.rename = makeButton("Rename", "rename");
    ui.cut = makeButton("Cut", "cut");
    ui.copy = makeButton("Copy", "copy");
    ui.paste = makeButton("Paste", "paste");
    ui.delete = makeButton("Delete", "delete", { danger: true });
    [ui.up, ui.newFolder, ui.newFile, ui.upload, ui.download, ui.rename, ui.cut, ui.copy, ui.paste, ui.delete].forEach((b) => left.append(b));

    ui.viewToggle = document.createElement("button");
    ui.viewToggle.type = "button";
    ui.viewToggle.className = "button button-small";
    ui.viewToggle.dataset.action = "view";
    right.append(ui.viewToggle);

    ui.crumbs = document.createElement("nav");
    ui.crumbs.className = "breadcrumbs explorer-crumbs";

    ui.status = document.createElement("div");
    ui.status.className = "explorer-status muted";

    ui.grid = document.createElement("div");
    ui.grid.className = "panel file-panel explorer-grid";

    ui.overlay = document.createElement("div");
    ui.overlay.className = "explorer-drop";
    ui.overlay.textContent = "Drop files to upload here";

    ui.uploadInput = document.createElement("input");
    ui.uploadInput.type = "file";
    ui.uploadInput.multiple = true;
    ui.uploadInput.className = "explorer-file-input";

    const stage = document.createElement("div");
    stage.className = "explorer-stage";
    stage.append(ui.grid, ui.overlay);

    root.append(toolbar, ui.crumbs, ui.status, stage, ui.uploadInput);
  };

  const renderCrumbs = () => {
    ui.crumbs.replaceChildren();
    (state.listing.breadcrumbs || []).forEach((crumb, index) => {
      if (index > 0) {
        const sep = document.createElement("span");
        sep.textContent = "/";
        ui.crumbs.append(sep);
      }
      const link = document.createElement("a");
      link.href = "#";
      link.textContent = crumb.name;
      link.dataset.crumb = crumb.path;
      link.addEventListener("click", (event) => { event.preventDefault(); load(crumb.path); });
      registerDropTarget(link, () => crumb.path);
      ui.crumbs.append(link);
    });
  };

  const iconFor = (entry) => {
    const span = document.createElement("span");
    span.className = `file-icon ${entry.is_dir ? "folder" : "document"}`;
    span.textContent = entry.is_dir ? "◆" : "▪";
    return span;
  };

  const buildRow = (entry, index) => {
    const row = document.createElement("div");
    row.className = "file-row explorer-item";
    row.draggable = true;
    row.dataset.path = entry.path;
    row.dataset.index = String(index);
    if (state.selection.has(entry.path)) row.classList.add("selected");
    if (state.clipboard && state.clipboard.op === "cut" && state.clipboard.paths.includes(entry.path)) {
      row.classList.add("cut");
    }

    const nameCell = document.createElement("span");
    nameCell.className = "file-name";
    const label = document.createElement("span");
    label.className = "file-label";
    label.textContent = entry.name;
    nameCell.append(iconFor(entry), label);
    row.append(nameCell);

    if (state.view === "details") {
      const size = document.createElement("span");
      size.textContent = entry.is_dir ? "—" : formatBytes(entry.size);
      const modified = document.createElement("span");
      modified.textContent = entry.modified_at || "—";
      const tags = document.createElement("span");
      tags.className = "file-tags";
      if (entry.editable) {
        const tag = document.createElement("span");
        tag.className = "file-tag";
        tag.textContent = "editable";
        tags.append(tag);
      }
      row.append(size, modified, tags);
    }

    wireItem(row, entry, index, label);
    return row;
  };

  const renderGrid = () => {
    ui.grid.replaceChildren();
    ui.grid.classList.toggle("view-icons", state.view === "icons");
    ui.grid.classList.toggle("view-details", state.view === "details");

    if (state.view === "details") {
      const header = document.createElement("div");
      header.className = "file-row file-header";
      ["Name", "Size", "Modified", ""].forEach((text) => {
        const span = document.createElement("span");
        span.textContent = text;
        header.append(span);
      });
      ui.grid.append(header);
    }

    if (state.entries.length === 0) {
      const empty = document.createElement("div");
      empty.className = "file-empty";
      empty.textContent = "This folder is empty. Drop files here or use New file / New folder.";
      ui.grid.append(empty);
      return;
    }
    state.entries.forEach((entry, index) => ui.grid.append(buildRow(entry, index)));
  };

  const updateToolbar = () => {
    const count = state.selection.size;
    const single = count === 1 ? entryByPath([...state.selection][0]) : null;
    ui.up.disabled = state.listing.is_root;
    ui.download.disabled = count !== 1;
    ui.rename.disabled = count !== 1;
    ui.cut.disabled = count === 0;
    ui.copy.disabled = count === 0;
    ui.delete.disabled = count === 0;
    ui.paste.disabled = !state.clipboard;
    ui.viewToggle.textContent = state.view === "details" ? "▦ Icons" : "≣ Details";
    if (single && single.is_dir) ui.download.textContent = "Download ZIP";
    else ui.download.textContent = "Download";

    const parts = [`${state.entries.length} item${state.entries.length === 1 ? "" : "s"}`];
    if (count > 0) parts.push(`${count} selected`);
    if (state.clipboard) parts.push(`${state.clipboard.paths.length} ${state.clipboard.op === "cut" ? "cut" : "copied"} to clipboard`);
    ui.status.textContent = parts.join(" · ");
  };

  const refreshSelectionStyles = () => {
    ui.grid.querySelectorAll(".explorer-item").forEach((row) => {
      row.classList.toggle("selected", state.selection.has(row.dataset.path));
    });
    updateToolbar();
  };

  // --- selection -----------------------------------------------------------
  const selectOnly = (path) => { state.selection = new Set([path]); };
  const selectRange = (toIndex) => {
    const from = state.lastIndex == null ? toIndex : state.lastIndex;
    const [lo, hi] = from <= toIndex ? [from, toIndex] : [toIndex, from];
    state.selection = new Set();
    for (let i = lo; i <= hi; i += 1) state.selection.add(state.entries[i].path);
  };
  const handleSelectClick = (entry, index, event) => {
    if (event.shiftKey && state.lastIndex != null) {
      selectRange(index);
    } else if (event.ctrlKey || event.metaKey) {
      if (state.selection.has(entry.path)) state.selection.delete(entry.path);
      else state.selection.add(entry.path);
      state.lastIndex = index;
    } else {
      selectOnly(entry.path);
      state.lastIndex = index;
    }
    refreshSelectionStyles();
  };

  // --- item behavior -------------------------------------------------------
  const openEntry = (entry) => {
    if (entry.is_dir) { load(entry.path); return; }
    if (entry.editable) { window.location.assign(`${base}/edit?path=${encodeURIComponent(entry.path)}`); return; }
    window.location.assign(`${base}/download?path=${encodeURIComponent(entry.path)}`);
  };

  const wireItem = (row, entry, index, label) => {
    row.addEventListener("click", (event) => handleSelectClick(entry, index, event));
    row.addEventListener("dblclick", () => openEntry(entry));
    row.addEventListener("contextmenu", (event) => {
      event.preventDefault();
      if (!state.selection.has(entry.path)) { selectOnly(entry.path); state.lastIndex = index; refreshSelectionStyles(); }
      openContextMenu(event.clientX, event.clientY);
    });
    row.addEventListener("dragstart", (event) => {
      if (!state.selection.has(entry.path)) { selectOnly(entry.path); state.lastIndex = index; refreshSelectionStyles(); }
      event.dataTransfer.setData(MOVE_TYPE, JSON.stringify([...state.selection]));
      event.dataTransfer.effectAllowed = "copyMove";
      row.classList.add("dragging");
    });
    row.addEventListener("dragend", () => row.classList.remove("dragging"));
    if (entry.is_dir) registerDropTarget(row, () => entry.path);
    row.querySelector(".file-label").addEventListener("dblclick", (event) => event.stopPropagation());
    label.title = entry.name;
  };

  const registerDropTarget = (element, getPath) => {
    element.addEventListener("dragover", (event) => {
      if (!Array.from(event.dataTransfer.types).includes(MOVE_TYPE)) return;
      event.preventDefault();
      event.stopPropagation();
      event.dataTransfer.dropEffect = event.ctrlKey || event.metaKey || event.altKey ? "copy" : "move";
      element.classList.add("drop-target");
    });
    element.addEventListener("dragleave", () => element.classList.remove("drop-target"));
    element.addEventListener("drop", (event) => {
      if (!Array.from(event.dataTransfer.types).includes(MOVE_TYPE)) return;
      event.preventDefault();
      event.stopPropagation();
      element.classList.remove("drop-target");
      let paths = [];
      try { paths = JSON.parse(event.dataTransfer.getData(MOVE_TYPE)); } catch (_) { return; }
      const dest = getPath();
      if (paths.includes(dest)) return;
      transfer(paths, dest, event.ctrlKey || event.metaKey || event.altKey);
    });
  };

  // --- actions -------------------------------------------------------------
  const transfer = async (paths, dest, copyMode) => {
    try {
      const data = await post(`${apiBase}/${copyMode ? "copy" : "move"}`, { target: paths, dest });
      if (state.clipboard && !copyMode) state.clipboard = null;
      await load(state.path);
      if (data.error) showToast(data.error, true);
      else showToast(`${copyMode ? "Copied" : "Moved"} ${paths.length} item${paths.length === 1 ? "" : "s"}.`);
    } catch (error) { showToast(error.message, true); }
  };

  const renameSelected = () => {
    if (state.selection.size !== 1) return;
    const entry = entryByPath([...state.selection][0]);
    const row = ui.grid.querySelector(`.explorer-item[data-path="${cssEscape(entry.path)}"]`);
    if (!row) return;
    startInlineRename(row, entry);
  };

  const startInlineRename = (row, entry) => {
    const label = row.querySelector(".file-label");
    if (!label || row.querySelector(".rename-input")) return;
    const input = document.createElement("input");
    input.type = "text";
    input.className = "rename-input";
    input.value = entry.name;
    label.replaceWith(input);
    input.focus();
    const dot = entry.is_dir ? -1 : entry.name.lastIndexOf(".");
    input.setSelectionRange(0, dot > 0 ? dot : entry.name.length);
    let done = false;
    const finish = async (commit) => {
      if (done) return;
      done = true;
      const value = input.value.trim();
      if (!commit || value === "" || value === entry.name) { load(state.path); return; }
      try {
        const data = await post(`${apiBase}/rename`, { target: entry.path, name: value });
        await load(state.path);
        if (data.path) { state.selection = new Set([data.path]); refreshSelectionStyles(); }
        showToast("Renamed.");
      } catch (error) { showToast(error.message, true); load(state.path); }
    };
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") { event.preventDefault(); finish(true); }
      else if (event.key === "Escape") { event.preventDefault(); finish(false); }
      event.stopPropagation();
    });
    input.addEventListener("blur", () => finish(true));
    input.addEventListener("click", (event) => event.stopPropagation());
  };

  const deleteSelected = async () => {
    if (state.selection.size === 0) return;
    const paths = [...state.selection];
    const hasFolder = paths.some((path) => entryByPath(path)?.is_dir);
    const message = hasFolder
      ? `Permanently delete ${paths.length} item${paths.length === 1 ? "" : "s"}? Folders and ALL of their contents will be deleted. This cannot be undone.`
      : `Permanently delete ${paths.length} item${paths.length === 1 ? "" : "s"}? This cannot be undone.`;
    if (!window.confirm(message)) return;
    try {
      const data = await post(`${apiBase}/delete`, { target: paths, recursive: hasFolder ? "true" : "false" });
      await load(state.path);
      if (data.error) showToast(data.error, true);
      else showToast(`Deleted ${data.succeeded} item${data.succeeded === 1 ? "" : "s"}.`);
    } catch (error) { showToast(error.message, true); }
  };

  const newFolder = async () => {
    const name = window.prompt("New folder name");
    if (!name) return;
    try { await post(`${apiBase}/new-folder`, { path: state.path || ".", name }); await load(state.path); showToast("Folder created."); }
    catch (error) { showToast(error.message, true); }
  };

  const newFile = async () => {
    const name = window.prompt("New file name");
    if (!name) return;
    try {
      const data = await post(`${apiBase}/new-file`, { path: state.path || ".", name });
      await load(state.path);
      if (data.path) { state.selection = new Set([data.path]); refreshSelectionStyles(); }
      showToast("File created.");
    } catch (error) { showToast(error.message, true); }
  };

  const uploadFiles = async (fileList) => {
    const files = Array.from(fileList || []);
    if (files.length === 0) return;
    const body = new FormData();
    body.append("path", state.path || ".");
    files.forEach((file) => body.append("files", file));
    try {
      const response = await fetch(`${apiBase}/upload`, {
        method: "POST",
        headers: { "X-CSRF-Token": csrf, Accept: "application/json" },
        body,
      });
      if (response.status === 401) { window.location.assign("/login?error=Please+sign+in+to+continue."); return; }
      const data = await response.json().catch(() => ({}));
      if (!response.ok && !data.ok) throw new Error(data.error || "Upload failed");
      await load(state.path);
      if (data.error) showToast(data.error, true);
      else showToast(`Uploaded ${data.succeeded} file${data.succeeded === 1 ? "" : "s"}.`);
    } catch (error) { showToast(error.message, true); }
  };

  const downloadSelected = () => {
    if (state.selection.size !== 1) return;
    const entry = entryByPath([...state.selection][0]);
    const route = entry.is_dir ? "download-zip" : "download";
    window.location.assign(`${base}/${route}?path=${encodeURIComponent(entry.path)}`);
  };

  const copyToClipboard = (op) => {
    if (state.selection.size === 0) return;
    state.clipboard = { op, paths: [...state.selection] };
    renderGrid();
    refreshSelectionStyles();
    showToast(`${op === "cut" ? "Cut" : "Copied"} ${state.clipboard.paths.length} item${state.clipboard.paths.length === 1 ? "" : "s"}.`);
  };

  const paste = () => {
    if (!state.clipboard) return;
    transfer(state.clipboard.paths, state.path || ".", state.clipboard.op === "copy");
  };

  // --- context menu --------------------------------------------------------
  let contextMenu = null;
  const closeContextMenu = () => { if (contextMenu) { contextMenu.remove(); contextMenu = null; } };
  const openContextMenu = (x, y) => {
    closeContextMenu();
    contextMenu = document.createElement("div");
    contextMenu.className = "context-menu";
    const count = state.selection.size;
    const single = count === 1 ? entryByPath([...state.selection][0]) : null;
    const items = [];
    if (single) {
      items.push({ label: single.is_dir ? "Open" : (single.editable ? "Edit" : "Download"), action: () => openEntry(single) });
      items.push({ label: single.is_dir ? "Download ZIP" : "Download", action: downloadSelected });
      items.push({ label: "Rename", action: renameSelected });
      items.push({ divider: true });
    }
    if (count > 0) {
      items.push({ label: "Cut", action: () => copyToClipboard("cut") });
      items.push({ label: "Copy", action: () => copyToClipboard("copy") });
    }
    items.push({ label: "Paste", action: paste, disabled: !state.clipboard });
    if (count > 0) {
      items.push({ divider: true });
      items.push({ label: "Delete", action: deleteSelected, danger: true });
    }
    if (count === 0) {
      items.push({ label: "New folder", action: newFolder });
      items.push({ label: "New file", action: newFile });
      items.push({ label: "Upload files", action: () => ui.uploadInput.click() });
      items.push({ divider: true });
      items.push({ label: "Refresh", action: () => load(state.path) });
      items.push({ label: "Select all", action: selectAll });
    }
    items.forEach((item) => {
      if (item.divider) { const d = document.createElement("div"); d.className = "context-divider"; contextMenu.append(d); return; }
      const button = document.createElement("button");
      button.type = "button";
      button.className = `context-item${item.danger ? " danger" : ""}`;
      button.textContent = item.label;
      button.disabled = !!item.disabled;
      button.addEventListener("click", () => { closeContextMenu(); item.action(); });
      contextMenu.append(button);
    });
    document.body.append(contextMenu);
    const rect = contextMenu.getBoundingClientRect();
    const left = Math.min(x, window.innerWidth - rect.width - 8);
    const top = Math.min(y, window.innerHeight - rect.height - 8);
    contextMenu.style.left = `${Math.max(8, left)}px`;
    contextMenu.style.top = `${Math.max(8, top)}px`;
  };

  const selectAll = () => { state.selection = new Set(state.entries.map((entry) => entry.path)); refreshSelectionStyles(); };

  // --- data load -----------------------------------------------------------
  const load = async (path) => {
    try {
      const listing = await fetchJSON(`${apiBase}?path=${encodeURIComponent(path || "")}`);
      state.listing = listing;
      state.path = listing.is_root ? "" : listing.current_path;
      state.entries = listing.entries || [];
      state.selection = new Set();
      state.lastIndex = null;
      const url = new URL(window.location.href);
      if (state.path) url.searchParams.set("path", state.path); else url.searchParams.delete("path");
      window.history.replaceState({}, "", url);
      renderCrumbs();
      renderGrid();
      updateToolbar();
    } catch (error) { showToast(error.message, true); }
  };

  // --- toolbar + global wiring --------------------------------------------
  buildShell();
  root.addEventListener("click", (event) => {
    const button = event.target.closest("[data-action]");
    if (!button) return;
    const actions = {
      up: () => state.listing && !state.listing.is_root && load(state.listing.parent_path),
      "new-folder": newFolder,
      "new-file": newFile,
      upload: () => ui.uploadInput.click(),
      download: downloadSelected,
      rename: renameSelected,
      cut: () => copyToClipboard("cut"),
      copy: () => copyToClipboard("copy"),
      paste,
      delete: deleteSelected,
      view: () => {
        state.view = state.view === "details" ? "icons" : "details";
        window.localStorage.setItem("vpsdeck-file-view", state.view);
        renderGrid();
        updateToolbar();
      },
    };
    const handler = actions[button.dataset.action];
    if (handler) handler();
  });

  ui.uploadInput.addEventListener("change", () => { uploadFiles(ui.uploadInput.files); ui.uploadInput.value = ""; });

  ui.grid.addEventListener("click", (event) => {
    if (event.target === ui.grid || event.target.classList.contains("file-empty") || event.target.classList.contains("file-header")) {
      state.selection = new Set();
      refreshSelectionStyles();
    }
  });

  // external (OS) file drop → upload to current folder
  let dragDepth = 0;
  root.addEventListener("dragenter", (event) => {
    if (!Array.from(event.dataTransfer.types).includes("Files")) return;
    dragDepth += 1;
    root.classList.add("drop-upload");
  });
  root.addEventListener("dragover", (event) => {
    if (!Array.from(event.dataTransfer.types).includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  });
  root.addEventListener("dragleave", (event) => {
    if (!Array.from(event.dataTransfer.types).includes("Files")) return;
    dragDepth -= 1;
    if (dragDepth <= 0) { dragDepth = 0; root.classList.remove("drop-upload"); }
  });
  root.addEventListener("drop", (event) => {
    if (!Array.from(event.dataTransfer.types).includes("Files")) return;
    event.preventDefault();
    dragDepth = 0;
    root.classList.remove("drop-upload");
    uploadFiles(event.dataTransfer.files);
  });

  document.addEventListener("click", closeContextMenu);
  document.addEventListener("scroll", closeContextMenu, true);
  window.addEventListener("resize", closeContextMenu);

  document.addEventListener("keydown", (event) => {
    const tag = (document.activeElement && document.activeElement.tagName) || "";
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    const ctrl = event.ctrlKey || event.metaKey;
    if (ctrl && event.key.toLowerCase() === "a") { event.preventDefault(); selectAll(); }
    else if (ctrl && event.key.toLowerCase() === "c") { copyToClipboard("copy"); }
    else if (ctrl && event.key.toLowerCase() === "x") { copyToClipboard("cut"); }
    else if (ctrl && event.key.toLowerCase() === "v") { paste(); }
    else if (event.key === "Delete") { deleteSelected(); }
    else if (event.key === "F2") { event.preventDefault(); renameSelected(); }
    else if (event.key === "Enter" && state.selection.size === 1) { openEntry(entryByPath([...state.selection][0])); }
    else if (event.key === "Backspace" && state.listing && !state.listing.is_root) { event.preventDefault(); load(state.listing.parent_path); }
  });

  load(state.path);
};

const cssEscape = (value) => (window.CSS && CSS.escape ? CSS.escape(value) : value.replace(/["\\]/g, "\\$&"));

const renderUpdate = (data) => {
  const container = document.querySelector("[data-monitor-update]");
  if (!container) return;
  const setText = (selector, value) => {
    const element = container.querySelector(selector);
    if (element) element.textContent = value;
  };
  setText("[data-update-current]", data.current_short || "unknown");
  setText("[data-update-remote]", data.remote_short || "—");
  setText("[data-update-checked]", data.checked_at ? formatTime(data.checked_at) : "—");

  const error = container.querySelector("[data-update-error]");
  if (error) { error.hidden = !data.error; error.textContent = data.error || ""; }

  const running = !!(data.apply && data.apply.state === "running");
  const apply = container.querySelector("[data-update-apply]");
  if (apply) {
    apply.hidden = !data.apply;
    setText("[data-update-state]", data.apply ? data.apply.state : "");
    setText("[data-update-message]", data.apply ? (data.apply.message || "") : "");
  }
  const button = container.querySelector("[data-update-apply-btn]");
  if (button) button.disabled = !data.available || running;

  const banner = container.querySelector("[data-update-banner]");
  if (banner) {
    banner.hidden = false;
    if (running) {
      banner.className = "update-banner update-banner-info";
      banner.textContent = "Updating VPSDeck… the panel may briefly restart.";
    } else if (data.requested) {
      banner.className = "update-banner update-banner-info";
      banner.textContent = "Update requested. The system updater will apply it shortly.";
    } else if (data.available) {
      banner.className = "update-banner update-banner-warn";
      banner.textContent = `Update available: ${data.current_short || "?"} → ${data.remote_short || "?"}.`;
    } else if (data.apply && data.apply.state === "failed") {
      banner.className = "update-banner update-banner-warn";
      banner.textContent = "The last update failed and was rolled back.";
    } else if (data.error) {
      banner.hidden = true;
    } else {
      banner.className = "update-banner update-banner-ok";
      banner.textContent = "VPSDeck is up to date.";
    }
  }
};

const setupUpdatePolling = () => {
  const container = document.querySelector("[data-monitor-update]");
  if (!container) return;
  const refresh = Math.max(Number(container.dataset.refresh || 5) * 1000, 5000);
  const tick = () => fetchJSON("/api/system/update").then(renderUpdate).catch(() => {});
  tick();
  window.setInterval(tick, refresh);
};

const setupGitHubPicker = () => {
  const picker = document.querySelector("[data-github-picker]");
  const form = document.querySelector("[data-github-form]");
  if (!picker || !form) return;

  const search = picker.querySelector("[data-github-search]");
  const list = picker.querySelector("[data-github-repos]");
  const ownerField = form.querySelector("[data-github-owner]");
  const repoField = form.querySelector("[data-github-repo]");
  const privateField = form.querySelector("[data-github-private]");
  const selected = form.querySelector("[data-github-selected]");
  const nameField = form.querySelector("[data-github-name]");
  const branchSelect = form.querySelector("[data-github-branch]");
  const submit = form.querySelector("[data-github-submit]");
  let timer = null;

  const loadRepos = async (query) => {
    list.replaceChildren();
    const loading = document.createElement("div");
    loading.className = "folder-loading";
    loading.textContent = "Loading repositories…";
    list.append(loading);
    try {
      const data = await fetchJSON(`/api/integrations/github/repos?q=${encodeURIComponent(query || "")}`);
      list.replaceChildren();
      const repos = data.repos || [];
      if (repos.length === 0) {
        const empty = document.createElement("div");
        empty.className = "folder-loading";
        empty.textContent = "No repositories matched.";
        list.append(empty);
        return;
      }
      repos.forEach((repo) => list.append(repoRow(repo)));
    } catch (error) {
      list.replaceChildren();
      const message = document.createElement("div");
      message.className = "alert alert-error";
      message.textContent = error.message;
      list.append(message);
    }
  };

  const repoRow = (repo) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "github-repo";
    const top = document.createElement("div");
    top.className = "github-repo-top";
    const name = document.createElement("strong");
    name.textContent = repo.full_name;
    top.append(name);
    if (repo.private) {
      const badge = document.createElement("span");
      badge.className = "github-badge";
      badge.textContent = "private";
      top.append(badge);
    }
    button.append(top);
    if (repo.description) {
      const description = document.createElement("p");
      description.textContent = repo.description;
      button.append(description);
    }
    button.addEventListener("click", () => selectRepo(repo, button));
    return button;
  };

  const selectRepo = async (repo, element) => {
    list.querySelectorAll(".github-repo").forEach((row) => row.classList.remove("active"));
    element.classList.add("active");
    ownerField.value = repo.owner;
    repoField.value = repo.name;
    privateField.value = repo.private ? "true" : "false";
    selected.value = repo.full_name;
    if (!nameField.value.trim()) nameField.value = repo.name;
    submit.disabled = true;

    branchSelect.disabled = true;
    branchSelect.replaceChildren(new Option("Loading branches…", ""));
    try {
      const data = await fetchJSON(`/api/integrations/github/branches?owner=${encodeURIComponent(repo.owner)}&repo=${encodeURIComponent(repo.name)}`);
      const branches = data.branches || [];
      branchSelect.replaceChildren();
      branches.forEach((branch) => branchSelect.append(new Option(branch, branch, branch === repo.default_branch, branch === repo.default_branch)));
      if (branches.length === 0) branchSelect.append(new Option(repo.default_branch || "main", repo.default_branch || "main", true, true));
      branchSelect.disabled = false;
      submit.disabled = false;
    } catch (error) {
      branchSelect.replaceChildren(new Option(repo.default_branch || "main", repo.default_branch || "main", true, true));
      branchSelect.disabled = false;
      submit.disabled = false;
      showToast(error.message, true);
    }
  };

  search.addEventListener("input", () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => loadRepos(search.value), 250);
  });
  loadRepos("");
};

document.addEventListener("DOMContentLoaded", () => {
  const list = document.querySelector("[data-env-list]");
  const template = document.querySelector("[data-env-template]");
  if (list && template && list.children.length === 0) {
    list.append(template.content.cloneNode(true));
  }
  setupFolderPicker();
  setupMonitorPolling();
  setupFileExplorer();
  setupUpdatePolling();
  setupGitHubPicker();
});
