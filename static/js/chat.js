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

function connect() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  socket = new WebSocket(`${protocol}//${location.host}/room?room=${room}`);

  socket.onopen = () => {
    console.log("WebSocket connection established.");
    // Reset the retry timeout on a successful connection
    retryTimeout = 1000;
    statusDiv.style.display = "none";
  };

  socket.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);

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
    console.log(`WebSocket disconnected. Retrying in ${retryTimeout / 1000}s...`);
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
  if (socket && socket.readyState === WebSocket.OPEN && input.value.trim() !== "") {
    socket.send(input.value);
    input.value = "";
  } else {
    console.error("Cannot send message: WebSocket is not open.");
    // You could also display a small warning to the user here.
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