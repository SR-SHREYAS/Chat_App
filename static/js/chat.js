const params = new URLSearchParams(window.location.search);
const room = params.get("room");

if (!room) {
  alert("No room specified. Redirecting to homepage...");
  window.location.href = "/";
}

// Create a status banner for connection updates
const statusDiv = document.createElement("div");
statusDiv.style.cssText = "position:fixed; top:0; left:0; width:100%; background:rgba(220, 53, 69, 0.9); color:white; text-align:center; padding:10px; display:none; z-index:1000; font-family: Arial, sans-serif; font-weight: bold; transition: all 0.3s ease;";
document.body.appendChild(statusDiv);

let socket;
let retryTimeout = 1000; // Start with a 1-second retry delay
let roomPassword = null;

function connect() {
  // Prompt for password only if we haven't stored it yet
  if (roomPassword === null) {
    roomPassword = prompt("Please enter the room password:", "");
    if (roomPassword === null) {
      alert("Password is required to join or create a private room.");
      return; // Stop connection attempt
    }
  }

  // Generate a simple random ID for the user for this session
  const userId = Math.random().toString(36).substring(2, 9);

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  socket = new WebSocket(`${protocol}//${location.host}/room?room=${room}&user_id=${userId}`);

  socket.onopen = () => {
    console.log("WebSocket connection established.");
    // Send authentication message immediately
    socket.send(JSON.stringify({
      type: "auth",
      password: roomPassword
    }));

    // Reset the retry timeout on a successful connection
    retryTimeout = 1000;
    statusDiv.style.display = "none";
    document.getElementById("msg").disabled = false;
    document.getElementById("sendBtn").disabled = false;
  };

  socket.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);

      // Handle System messages differently
      if (data.name === "System") {
        const sysDiv = document.createElement("div");
        sysDiv.classList.add("system-message");
        sysDiv.textContent = data.message;
        document.getElementById("messages").appendChild(sysDiv);
        const messagesDiv = document.getElementById("messages");
        messagesDiv.scrollTop = messagesDiv.scrollHeight;
        return;
      }

      // Create the container div
      const msgContainer = document.createElement("div");
      msgContainer.classList.add("message-container");

      // Create the username div
      const usernameDiv = document.createElement("div");
      usernameDiv.classList.add("username");
      usernameDiv.textContent = data.name;

      // Create the message div
      const messageDiv = document.createElement("div");
      messageDiv.classList.add("message");
      messageDiv.textContent = data.message;

      // Append username and message in correct order
      msgContainer.appendChild(usernameDiv);
      msgContainer.appendChild(messageDiv);

      // Append the whole message container to the messages div
      document.getElementById("messages").appendChild(msgContainer);

      // Auto-scroll
      const messagesDiv = document.getElementById("messages");
      messagesDiv.scrollTop = messagesDiv.scrollHeight;
    } catch (err) {
      console.error("Invalid JSON received:", event.data);
    }
  };

  socket.onclose = (event) => {
    document.getElementById("msg").disabled = true;
    document.getElementById("sendBtn").disabled = true;

    const { code, reason, wasClean } = event;
    // 1000: Normal Closure, 1008: Policy Violation, 1011: Internal Error
    const nonRetryableCodes = [1000, 1008, 1011];

    if (wasClean || nonRetryableCodes.includes(code)) {
      statusDiv.innerText = "Connection closed. Please refresh the page to reconnect.";
      statusDiv.style.display = "block";
      return;
    }

    console.log(`WebSocket disconnected (code: ${code}, reason: "${reason || "none"}"). Retrying in ${retryTimeout / 1000}s...`);
    statusDiv.innerText = `Connection lost. Reconnecting in ${retryTimeout / 1000}s...`;
    statusDiv.style.display = "block";
    // Schedule the next reconnection attempt
    setTimeout(connect, retryTimeout);
    // Implement exponential backoff: double the delay each time, up to a max
    retryTimeout = Math.min(retryTimeout * 2, 30000); // Cap at 30 seconds
  };

  socket.onerror = (error) => {
    console.error("WebSocket error:", error);
    // The 'onclose' event will fire automatically after an error,
    // which will trigger the reconnection logic.
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
    statusDiv.innerText = "Message not sent. You are currently disconnected.";
    statusDiv.style.display = "block";
  }
}

document.getElementById("sendBtn").addEventListener("click", sendMessage);

document.getElementById("msg").addEventListener("keyup", function (event) {
  if (event.key === "Enter") {
    sendMessage();
  }
});

// Initial connection attempt
connect();