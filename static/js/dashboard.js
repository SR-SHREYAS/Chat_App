const displayNameText = document.getElementById("displayNameText");
const editNameBtn = document.getElementById("editNameBtn");
const editNamePanel = document.getElementById("editNamePanel");
const displayNameInput = document.getElementById("displayNameInput");
const saveNameBtn = document.getElementById("saveNameBtn");

const signoutBtn = document.getElementById("signoutBtn");

const roomInput = document.getElementById("roomInput");
const ttlMinutesInput = document.getElementById("ttlMinutesInput");
const createRoomBtn = document.getElementById("createRoomBtn");
const joinRoomBtn = document.getElementById("joinRoomBtn");

const historyTitle = document.getElementById("historyTitle");
const historySlide = document.getElementById("historySlide");
const historyPrevBtn = document.getElementById("historyPrevBtn");
const historyNextBtn = document.getElementById("historyNextBtn");

const dashboardMessage = document.getElementById("dashboardMessage");

const state = {
  slideIndex: 0,
  ownedRooms: [],
  preferredRoomAction: "join",
  ttlConfig: {
    defaultMinutes: 10,
    maxMinutes: 10080,
  },
};

const slides = [
  {
    title: "Owned Rooms",
    heading: "Active Signed Rooms",
    text: "No active signed rooms yet. Create one to start the TTL flow.",
  },
  {
    title: "Room History",
    heading: "Shared Rooms",
    text: "This section will show rooms shared with you and quick re-entry links.",
  },
  {
    title: "Room History",
    heading: "Pinned Rooms",
    text: "This section will support pinned rooms for faster workflow.",
  },
];

function setMessage(text, isError = false) {
  dashboardMessage.textContent = text || "";
  dashboardMessage.classList.toggle("error", Boolean(isError && text));
}

function updateDisplayNameUI(name) {
  displayNameText.textContent = name;
  displayNameInput.value = name;
}

function formatExpiry(expiresAt) {
  const dt = new Date(expiresAt);
  if (Number.isNaN(dt.getTime())) {
    return "unknown";
  }
  return dt.toLocaleString();
}

function renderOwnedRoomsSlide() {
  const slide = slides[0];
  historyTitle.textContent = slide.title;
  historySlide.replaceChildren();

  const heading = document.createElement("h3");
  heading.textContent = slide.heading;
  historySlide.appendChild(heading);

  if (!state.ownedRooms.length) {
    const empty = document.createElement("p");
    empty.textContent = slide.text;
    historySlide.appendChild(empty);
    return;
  }

  const list = document.createElement("div");
  list.className = "owned-room-list";

  state.ownedRooms.forEach((room) => {
    const row = document.createElement("div");
    row.className = "owned-room-item";

    const name = document.createElement("strong");
    name.textContent = room.room_name;

    const expiry = document.createElement("span");
    expiry.textContent = `expires ${formatExpiry(room.expires_at)}`;

    row.append(name, expiry);
    list.appendChild(row);
  });

  historySlide.appendChild(list);
}

function renderStaticSlide(index) {
  const slide = slides[index];
  historyTitle.textContent = slide.title;
  historySlide.replaceChildren();

  const heading = document.createElement("h3");
  heading.textContent = slide.heading;

  const bodyText = document.createElement("p");
  bodyText.textContent = slide.text;

  historySlide.append(heading, bodyText);
}

function renderSlide() {
  if (state.slideIndex === 0) {
    renderOwnedRoomsSlide();
    return;
  }
  renderStaticSlide(state.slideIndex);
}

async function loadSession() {
  try {
    const res = await fetch("/api/auth/me", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) {
      throw new Error("Session check failed");
    }

    const payload = await res.json();
    if (!payload.authenticated || !payload.user) {
      window.location.href = "/";
      return false;
    }

    updateDisplayNameUI(payload.user.display_name);
    return true;
  } catch (_err) {
    window.location.href = "/";
    return false;
  }
}

async function loadOwnedRooms() {
  try {
    const res = await fetch("/api/rooms/owned", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });

    const payload = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(payload.error || "Could not load rooms");
    }

    state.ownedRooms = Array.isArray(payload.rooms) ? payload.rooms : [];
    renderSlide();
  } catch (err) {
    setMessage(err.message || "Could not load owned rooms", true);
  }
}

function normalizeRoomInput() {
  return roomInput.value.trim();
}

function parseTTLMinutes() {
  const raw = String(ttlMinutesInput.value || "").trim();
  if (!raw) {
    return null;
  }

  const minutes = Number.parseInt(raw, 10);
  if (!Number.isFinite(minutes) || minutes < 1) {
    return { error: "TTL must be a positive number" };
  }
  if (minutes > state.ttlConfig.maxMinutes) {
    return { error: `TTL cannot exceed ${state.ttlConfig.maxMinutes} minutes` };
  }
  return { value: minutes };
}

async function loadRoomConfig() {
  try {
    const res = await fetch("/api/rooms/config", {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    if (!res.ok) {
      return;
    }

    const payload = await res.json().catch(() => ({}));
    const max = Number.parseInt(payload.max_ttl_minutes, 10);
    const def = Number.parseInt(payload.default_ttl_minutes, 10);

    if (Number.isFinite(max) && max > 0) {
      state.ttlConfig.maxMinutes = max;
      ttlMinutesInput.max = String(max);
    }
    if (Number.isFinite(def) && def > 0) {
      state.ttlConfig.defaultMinutes = def;
      ttlMinutesInput.placeholder = String(def);
    }
  } catch (_err) {
    // Keep fallback values.
  }
}

async function createSignedRoom() {
  const room = normalizeRoomInput();
  if (!room) {
    setMessage("Please enter a room name.", true);
    return;
  }

  const ttlParsed = parseTTLMinutes();
  if (ttlParsed && ttlParsed.error) {
    setMessage(ttlParsed.error, true);
    return;
  }

  const body = { room_name: room };
  if (ttlParsed && ttlParsed.value) {
    body.ttl_minutes = ttlParsed.value;
  }

  setMessage("");
  try {
    const res = await fetch("/api/rooms/create", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify(body),
    });

    const payload = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(payload.error || "Could not create room");
    }

    if (payload.chat_url) {
      window.location.href = payload.chat_url;
      return;
    }

    throw new Error("Missing chat URL in response");
  } catch (err) {
    setMessage(err.message || "Could not create room", true);
  }
}

async function joinSignedRoom() {
  const room = normalizeRoomInput();
  if (!room) {
    setMessage("Please enter a room name.", true);
    return;
  }

  setMessage("");
  try {
    const res = await fetch("/api/rooms/join", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({ room_name: room }),
    });

    const payload = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(payload.error || "Could not join room");
    }

    if (payload.chat_url) {
      window.location.href = payload.chat_url;
      return;
    }

    throw new Error("Missing chat URL in response");
  } catch (err) {
    setMessage(err.message || "Could not join room", true);
  }
}

function runPreferredRoomAction() {
  if (state.preferredRoomAction === "create") {
    createSignedRoom();
    return;
  }
  joinSignedRoom();
}

editNameBtn.addEventListener("click", () => {
  editNamePanel.classList.toggle("hidden");
  if (!editNamePanel.classList.contains("hidden")) {
    displayNameInput.focus();
    displayNameInput.select();
  }
});

saveNameBtn.addEventListener("click", async () => {
  const displayName = displayNameInput.value.trim();
  if (!displayName) {
    setMessage("Display name cannot be empty.", true);
    return;
  }

  setMessage("");
  try {
    const res = await fetch("/api/auth/display-name", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({ display_name: displayName }),
    });
    const payload = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(payload.error || "Could not update display name");
    }
    if (payload.user && payload.user.display_name) {
      updateDisplayNameUI(payload.user.display_name);
      editNamePanel.classList.add("hidden");
      setMessage("Display name updated.");
      return;
    }
    throw new Error("Unexpected response");
  } catch (err) {
    setMessage(err.message || "Could not update display name", true);
  }
});

signoutBtn.addEventListener("click", async () => {
  try {
    await fetch("/api/auth/signout", {
      method: "POST",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
  } finally {
    window.location.href = "/";
  }
});

createRoomBtn.addEventListener("click", () => {
  state.preferredRoomAction = "create";
  createSignedRoom();
});

joinRoomBtn.addEventListener("click", () => {
  state.preferredRoomAction = "join";
  joinSignedRoom();
});

roomInput.addEventListener("keyup", (event) => {
  if (event.key === "Enter") {
    runPreferredRoomAction();
  }
});

historyPrevBtn.addEventListener("click", () => {
  state.slideIndex = (state.slideIndex - 1 + slides.length) % slides.length;
  renderSlide();
});

historyNextBtn.addEventListener("click", () => {
  state.slideIndex = (state.slideIndex + 1) % slides.length;
  renderSlide();
});

renderSlide();
loadRoomConfig();
loadSession().then((ok) => {
  if (ok) {
    loadOwnedRooms();
  }
});
