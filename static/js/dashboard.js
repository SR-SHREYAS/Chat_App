const displayNameText = document.getElementById("displayNameText");
const editNameBtn = document.getElementById("editNameBtn");
const editNamePanel = document.getElementById("editNamePanel");
const displayNameInput = document.getElementById("displayNameInput");
const saveNameBtn = document.getElementById("saveNameBtn");

const signoutBtn = document.getElementById("signoutBtn");

const roomInput = document.getElementById("roomInput");
const createRoomBtn = document.getElementById("createRoomBtn");
const joinRoomBtn = document.getElementById("joinRoomBtn");

const historyTitle = document.getElementById("historyTitle");
const historySlide = document.getElementById("historySlide");
const historyPrevBtn = document.getElementById("historyPrevBtn");
const historyNextBtn = document.getElementById("historyNextBtn");

const dashboardMessage = document.getElementById("dashboardMessage");

const slides = [
  {
    title: "Room History",
    heading: "Recent Rooms",
    text: "This section will show joined/owned room history and activity timeline.",
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

let slideIndex = 0;

function setMessage(text, isError = false) {
  dashboardMessage.textContent = text || "";
  dashboardMessage.classList.toggle("error", Boolean(isError && text));
}

function renderSlide() {
  const slide = slides[slideIndex];
  historyTitle.textContent = slide.title;
  historySlide.innerHTML = `<h3>${slide.heading}</h3><p>${slide.text}</p>`;
}

function updateDisplayNameUI(name) {
  displayNameText.textContent = name;
  displayNameInput.value = name;
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
      return;
    }

    updateDisplayNameUI(payload.user.display_name);
  } catch (err) {
    window.location.href = "/";
  }
}

function goToChatRoom() {
  const room = roomInput.value.trim();
  if (!room) {
    setMessage("Please enter a room name.", true);
    return;
  }
  const url = `/chat?room=${encodeURIComponent(room)}`;
  window.location.href = url;
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

createRoomBtn.addEventListener("click", goToChatRoom);
joinRoomBtn.addEventListener("click", goToChatRoom);
roomInput.addEventListener("keyup", (event) => {
  if (event.key === "Enter") {
    goToChatRoom();
  }
});

historyPrevBtn.addEventListener("click", () => {
  slideIndex = (slideIndex - 1 + slides.length) % slides.length;
  renderSlide();
});

historyNextBtn.addEventListener("click", () => {
  slideIndex = (slideIndex + 1) % slides.length;
  renderSlide();
});

renderSlide();
loadSession();

