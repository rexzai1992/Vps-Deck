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

document.addEventListener("DOMContentLoaded", () => {
  const list = document.querySelector("[data-env-list]");
  const template = document.querySelector("[data-env-template]");
  if (list && template && list.children.length === 0) {
    list.append(template.content.cloneNode(true));
  }
  setupFolderPicker();
  setupMonitorPolling();
});
