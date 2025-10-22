# Real-Time Chat Application in Go

This project is a real-time, room-based chat application built with a Go backend and a simple HTML/CSS/JavaScript frontend. It demonstrates the power of Go's concurrency model (goroutines and channels) and WebSockets to create efficient, real-time web applications.

## Core Concepts & Technologies

This project is built upon a few key technologies and programming concepts.

### 1. Go Backend (`net/http`)

The web server is built using Go's standard `net/http` package. It handles routing, serving static files (CSS, JS), and serving the initial HTML templates.

*   **What it is**: A powerful library for building HTTP servers and clients in Go.
*   **How it's used**: We define handlers for different URL paths.
    *   `/`: Serves the landing page (`index.html`) where a user can choose a room.
    *   `/chat`: Serves the main chat interface (`chat.html`).
    *   `/static/`: Serves static assets like CSS and JavaScript.
    *   `/room`: This is a special endpoint that handles the request to upgrade a standard HTTP connection to a WebSocket connection.

### 2. WebSockets (`gorilla/websocket`)

For real-time communication, we need a persistent, two-way connection between the client (browser) and the server. HTTP is not designed for this, so we "upgrade" the connection to a WebSocket.

*   **What it is**: A communication protocol that provides a full-duplex (two-way) communication channel over a single, long-lived TCP connection.
*   **How it's used**: When a user joins a chat room, the JavaScript client sends an upgrade request to the `/room` endpoint. The server uses the `gorilla/websocket` library to handle this handshake and establish the connection. Once established, both the client and server can send messages to each other at any time without needing to make new requests.

    ```go
    // From room.go: Upgrades the HTTP connection to a WebSocket
    var upgrader = &websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}

    func (r *room) ServeHTTP(w http.ResponseWriter, req *http.Request) {
        socket, err := upgrader.Upgrade(w, req, nil)
        // ... create a client and manage the connection
    }
    ```

### 3. Concurrency: Goroutines and Channels

This is the heart of the Go backend's design. Instead of blocking threads, Go uses a lightweight concurrency model that is highly efficient.

*   **What they are**:
    *   **Goroutines**: Extremely lightweight threads managed by the Go runtime. You can have thousands of them running concurrently.
    *   **Channels**: Typed "pipes" that allow goroutines to communicate with each other safely, without data races.
*   **How they are used**:
    *   Each chat room (`room`) runs in its own dedicated goroutine (`go room.run()`).
    *   Each connected user (`client`) has two dedicated goroutines: one for reading messages from the browser (`client.read()`) and one for writing messages to the browser (`client.write()`).
    *   The `room` struct uses channels to manage its state. This prevents race conditions by serializing all operations (join, leave, message broadcast) through a central loop.

    ```go
    // From room.go: The central logic loop for a single chat room.
    // This runs in its own goroutine and listens for activity on its channels.
    func (r *room) run() {
        for {
            select {
            case client := <-r.join:
                // A client wants to join: add them to the clients map.
                r.clients[client] = true
            case client := <-r.leave:
                // A client wants to leave: remove them and close their channel.
                delete(r.clients, client)
                close(client.receive)
            case msg := <-r.forward:
                // A message needs to be broadcast: send it to all connected clients.
                for client := range r.clients {
                    client.receive <- msg
                }
            }
        }
    }
    ```

### 4. Concurrency Safety (`sync.Mutex`)

When multiple goroutines need to access a shared resource (like the global map of all chat rooms), we need to prevent them from writing to it at the same time.

*   **What it is**: A "mutual exclusion lock". Only one goroutine can hold the lock at a time, forcing others to wait.
*   **How it's used**: We use a `sync.Mutex` to protect the global `rooms` map. When a user tries to join a room, we `Lock()` the mutex before checking if the room exists and `Unlock()` it after we are done. This ensures that two users can't accidentally try to create the same room at the exact same time, which would cause a data race.

    ```go
    // From room.go: Safely getting or creating a room.
    var rooms = make(map[string]*room)
    var mu sync.Mutex

    func getRoom(name string) *room {
        mu.Lock()
        defer mu.Unlock()

        if room, ok := rooms[name]; ok {
            return room // Room exists
        }
        // Room doesn't exist, create it
        room := newRoom()
        rooms[name] = room
        go room.run()
        return room
    }
    ```

## Flow of Execution

1.  **Join**: A user enters a room name on the homepage and is directed to `/chat?room=my-room`.
2.  **Connect**: The browser loads `chat.html`, and its JavaScript opens a WebSocket connection to the server's `/room` endpoint.
3.  **Upgrade**: The server's `room.ServeHTTP` handler upgrades the connection, creates a `client` object for this user, and adds the client to the appropriate `room` via the `join` channel.
4.  **Send Message**: The user types a message and hits send. The JavaScript sends the text over the WebSocket.
5.  **Read & Forward**: The `client.read()` goroutine on the server receives the text, wraps it in a JSON object with the username, and sends it to the `room.forward` channel.
6.  **Broadcast**: The `room.run()` goroutine receives the message from its `forward` channel and sends it to the `receive` channel of every client currently in that room.
7.  **Write & Display**: Each client's `write()` goroutine receives the message on its `receive` channel, sends it down the WebSocket to the browser, where JavaScript renders it on the screen.

## How to Run

1.  **Prerequisites**: Make sure you have Go installed.
2.  **Install Dependencies**: Open a terminal in the project root and run:
    ```bash
    go mod tidy
    ```
3.  **Run the Server**:
    ```bash
    go run .
    ```
4.  **Access the App**: Open your web browser and navigate to `http://localhost:8080`.

## Future Goals

-   **User Authentication**: Implement a proper user login system using a service like Auth0. The creator of a room (admin) could generate access tokens for others to join.
-   **Message Persistence**: Integrate a database like PostgreSQL to store chat history, allowing users to view older messages upon joining a room.
-   **File Transfers**: Add the ability for users to send and receive files (images, documents) within the chat, including a download feature.
-   **End-to-End Encryption (E2EE)**: Implement E2EE for private, one-on-one messaging between two users, ensuring that even the server cannot read the message content.