const params = new URLSearchParams(window.location.search);
const roomMode = resolveRoomMode(params);
let roomCode = "";

if (roomMode.error) {
  alert(roomMode.error);
  window.location.href = "/";
}

const roomTTLDiv = document.getElementById("roomTTL");
const roomCodeDiv = document.getElementById("roomCode");

let roomExpiryTime = parseExpiry(params.get("expires_at"));
let countdownTimer = null;
let roomExpired = false;

const statusDiv = document.createElement("div");
statusDiv.style.cssText = "position:fixed; top:0; left:0; width:100%; background:rgba(220, 53, 69, 0.9); color:white; text-align:center; padding:10px; display:none; z-index:1000; font-family: Arial, sans-serif; font-weight: bold; transition: all 0.3s ease;";
document.body.appendChild(statusDiv);

let socket;
let retryTimeout = 1000;

function showStatus(message) {
  statusDiv.innerText = message;
  statusDiv.style.display = "block";
}

function markRoomUnavailable(ttlText, statusMessage) {
  roomExpired = true;
  roomTTLDiv.classList.remove("hidden");
  roomTTLDiv.textContent = ttlText;
  disableInput();
  showStatus(statusMessage);
}

function isValidEntryCode(value) {
  return ChatEntryCode.isValidEntryCode(value);
}

function resolveRoomMode(params) {
  const hasSignedRoomParam = params.has("room_id");
  const hasUnsignedRoomParam = params.has("room");
  const signedRoomID = hasSignedRoomParam ? params.get("room_id") : "";
  const unsignedRoomName = hasUnsignedRoomParam ? params.get("room") : "";

  if (hasSignedRoomParam && hasUnsignedRoomParam) {
    return { error: "Ambiguous room link. Redirecting to homepage..." };
  }

  if (hasSignedRoomParam && !signedRoomID) {
    return { error: "Invalid room link. Redirecting to homepage..." };
  }

  if (hasUnsignedRoomParam && !unsignedRoomName) {
    return { error: "No room specified. Redirecting to homepage..." };
  }

  if (!hasSignedRoomParam && !hasUnsignedRoomParam) {
    return { error: "No room specified. Redirecting to homepage..." };
  }

  if (hasSignedRoomParam) {
    return {
      type: "signed",
      roomID: signedRoomID,
      queryParam: "room_id",
      queryValue: signedRoomID,
    };
  }

  return {
    type: "unsigned",
    roomID: null,
    queryParam: "room",
    queryValue: unsignedRoomName,
  };
}

function loadRoomEntryCode() {
  if (roomMode.type === "signed") {
    // Primary: key by stable signed room ID.
    let code = ChatEntryCode.loadEntryCodeForRoom(roomMode.roomID);

    // Backward compatibility: if nothing found, try legacy name-based key.
    if (!isValidEntryCode(code)) {
      const legacyKey = params.get("room_name") || params.get("room") || "";
      if (legacyKey) {
        code = ChatEntryCode.loadEntryCodeForRoom(legacyKey);
      }
    }

    roomCode = code;
    return;
  }

  roomCode = "";
}

function parseExpiry(raw) {
  if (!raw) {
    return null;
  }
  const dt = new Date(raw);
  if (Number.isNaN(dt.getTime())) {
    return null;
  }
  return dt;
}

function formatRemaining(totalSeconds) {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  if (hours > 0) {
    return `${hours}h ${minutes.toString().padStart(2, "0")}m ${seconds.toString().padStart(2, "0")}s`;
  }
  return `${minutes.toString().padStart(2, "0")}m ${seconds.toString().padStart(2, "0")}s`;
}

function disableInput() {
  document.getElementById("msg").disabled = true;
  document.getElementById("sendBtn").disabled = true;
}

function renderRoomCode() {
  if (!roomCodeDiv) {
    return;
  }
  if (!isValidEntryCode(roomCode)) {
    roomCodeDiv.classList.add("hidden");
    return;
  }
  roomCodeDiv.classList.remove("hidden");
  roomCodeDiv.textContent = `Entry code: ${roomCode}`;
}

function enableInput() {
  document.getElementById("msg").disabled = false;
  document.getElementById("sendBtn").disabled = false;
}

function renderTTLCountdown() {
  if (!roomExpiryTime) {
    roomTTLDiv.classList.add("hidden");
    return;
  }

  const remainingSeconds = Math.floor((roomExpiryTime.getTime() - Date.now()) / 1000);
  if (remainingSeconds <= 0) {
    markRoomUnavailable("Room TTL: expired", "This signed room has expired.");
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.close(1000, "room expired");
    }
    if (countdownTimer) {
      clearInterval(countdownTimer);
      countdownTimer = null;
    }
    return;
  }

  roomTTLDiv.classList.remove("hidden");
  roomTTLDiv.textContent = `Room TTL: ${formatRemaining(remainingSeconds)}`;
}

function startCountdown() {
  if (!roomExpiryTime) {
    return;
  }
  renderTTLCountdown();
  if (countdownTimer) {
    clearInterval(countdownTimer);
  }
  countdownTimer = setInterval(renderTTLCountdown, 1000);
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
    const loadedLength = Number.parseInt(payload.entry_code_length, 10);
    if (Number.isFinite(loadedLength) && loadedLength > 0) {
      ChatEntryCode.setEntryCodeLength(loadedLength);
    }
  } catch (_err) {
    // Keep fallback values.
  }
}

async function resolveRoomExpiryFromAPI() {
  if (roomMode.type !== "signed" || roomExpiryTime) {
    if (roomMode.type === "signed" && !isValidEntryCode(roomCode)) {
      markRoomUnavailable("Room TTL: restricted", "Entry code required to access this room.");
    }
    return;
  }

  try {
    const res = await fetch(`/api/rooms/status?room_id=${encodeURIComponent(roomMode.roomID)}`, {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });

    // For guests/non-signed rooms, TTL metadata can be unavailable; do not block chat.
    if (res.status === 401 || res.status === 403) {
      return;
    }

    if (res.status === 404 || res.status === 410) {
      markRoomUnavailable("Room TTL: expired", "This room has expired or does not exist.");
      return;
    }

    if (!res.ok) {
      return;
    }

    const payload = await res.json();
    if (payload.exists && payload.room && payload.room.expires_at) {
      if (!isValidEntryCode(roomCode)) {
        markRoomUnavailable("Room TTL: restricted", "Entry code required to access this room.");
        return;
      }
      const resolvedExpiry = parseExpiry(payload.room.expires_at);
      if (resolvedExpiry && resolvedExpiry.getTime() <= Date.now()) {
        markRoomUnavailable("Room TTL: expired", "This room has expired.");
        return;
      }
      roomExpiryTime = resolvedExpiry;
      startCountdown();
    }
  } catch (_err) {
    // If status cannot be resolved, chat can still continue as non-TTL room flow.
  }
}

function connect() {
  if (roomExpired) {
    return;
  }

  const userId = Math.random().toString(36).substring(2, 9);

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const qs = new URLSearchParams({
    user_id: userId,
  });
  qs.set(roomMode.queryParam, roomMode.queryValue);
  socket = new WebSocket(`${protocol}//${location.host}/room?${qs.toString()}`);

  socket.onopen = () => {
    if (isValidEntryCode(roomCode)) {
      socket.send(JSON.stringify({ type: "auth", entry_code: roomCode }));
    }
    console.log("WebSocket connection established.");
    retryTimeout = 1000;
    statusDiv.style.display = "none";
    enableInput();
  };

  socket.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);

      if (data.type === "error" && data.code === "message_too_long") {
        showStatus(`Message too long (max ${MAX_MESSAGE_LEN} characters).`);
        setTimeout(() => {
          if (!roomExpired && socket.readyState === WebSocket.OPEN) {
            statusDiv.style.display = "none";
          }
        }, 4000);
        return;
      }

      if (data.type === "error") {
        console.error("Chat error event:", data);
        const message =
          typeof data.message === "string" && data.message.trim().length > 0
            ? data.message
            : "An error occurred while sending your message. Please try again.";
        showStatus(message);
        setTimeout(() => {
          if (!roomExpired && socket.readyState === WebSocket.OPEN) {
            statusDiv.style.display = "none";
          }
        }, 4000);
        return;
      }

      if (data.name === "System") {
        const sysDiv = document.createElement("div");
        sysDiv.classList.add("system-message");
        sysDiv.textContent = data.message;
        document.getElementById("messages").appendChild(sysDiv);
        const messagesDiv = document.getElementById("messages");
        messagesDiv.scrollTop = messagesDiv.scrollHeight;
        return;
      }

      const msgContainer = document.createElement("div");
      msgContainer.classList.add("message-container");

      const usernameDiv = document.createElement("div");
      usernameDiv.classList.add("username");
      usernameDiv.textContent = data.name;

      const messageDiv = document.createElement("div");
      messageDiv.classList.add("message");
      messageDiv.textContent = data.message;

      msgContainer.appendChild(usernameDiv);
      msgContainer.appendChild(messageDiv);
      document.getElementById("messages").appendChild(msgContainer);

      const messagesDiv = document.getElementById("messages");
      messagesDiv.scrollTop = messagesDiv.scrollHeight;
    } catch (err) {
      console.error("Invalid JSON received:", event.data);
    }
  };

  socket.onclose = (event) => {
    disableInput();

    if (roomExpired) {
      showStatus("This signed room has expired.");
      return;
    }

    const { code, reason, wasClean } = event;
    const nonRetryableCodes = [1000, 1008, 1011];

    if (wasClean || nonRetryableCodes.includes(code)) {
      showStatus("Connection closed. Please refresh the page to reconnect.");
      return;
    }

    console.log(`WebSocket disconnected (code: ${code}, reason: "${reason || "none"}"). Retrying in ${retryTimeout / 1000}s...`);
    showStatus(`Connection lost. Reconnecting in ${retryTimeout / 1000}s...`);
    setTimeout(connect, retryTimeout);
    retryTimeout = Math.min(retryTimeout * 2, 30000);
  };

  socket.onerror = (error) => {
    console.error("WebSocket error:", error);
    socket.close();
  };
}

function sendMessage() {
  const input = document.getElementById("msg");
  if (socket && socket.readyState === WebSocket.OPEN) {
    if (input.value.trim() !== "") {
      socket.send(input.value);
      input.value = "";
    }
  } else {
    console.error("Cannot send message: WebSocket is not open.");
    showStatus("Message not sent. You are currently disconnected.");
  }
}

document.getElementById("sendBtn").addEventListener("click", sendMessage);

const MAX_MESSAGE_LEN = 2000;
const msgInput = document.getElementById("msg");

msgInput.maxLength = MAX_MESSAGE_LEN;
msgInput.addEventListener("input", () => {
  if (msgInput.value.length > MAX_MESSAGE_LEN) {
    msgInput.value = msgInput.value.slice(0, MAX_MESSAGE_LEN);
  }
});

msgInput.addEventListener("keyup", function (event) {
  if (event.key === "Enter") {
    sendMessage();
  }
});

if (!roomMode.error) {
  loadRoomConfig().finally(() => {
    loadRoomEntryCode();
    resolveRoomExpiryFromAPI().finally(() => {
      renderRoomCode();
      if (roomExpired) {
        return;
      }
      if (roomExpiryTime) {
        startCountdown();
      }
      connect();
    });
  });
}
