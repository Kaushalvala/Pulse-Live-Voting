# 🎯 Pulse - Real-Time Voting Application

Pulse is a simple yet powerful real-time polling application built with Go, Redis, and WebSockets. Create polls in seconds and see the results update live as votes come in.

![A GIF showing the Pulse application in action, from creating a poll to voting and seeing live results.](https://raw.githubusercontent.com/kaushalvala/Pulse-Live-Voting/main/demo.gif)
*(Note: You would replace the link above with a real screen recording or GIF of your application.)*

---

## Features

-   **Seamless Poll Creation**: An intuitive interface to create custom polls with a question and multiple options.
-   **Real-Time Results**: Vote counts update instantly for all participants using WebSockets.
-   **Shareable Links**: Each poll gets a unique, short, and shareable link upon creation.
-   **Duplicate Vote Prevention**: The backend prevents duplicate votes by tracking client IDs in a Redis set. The frontend uses `localStorage` to persist the client ID.
-   **Modern UI**: A clean, responsive, and animated user interface built with vanilla HTML, CSS, and JavaScript.
-   **Scalable Backend**: Built with Go and leverages Redis for efficient data storage and a Pub/Sub mechanism to broadcast updates.
-   **Ephemeral Polls**: Polls and their results are automatically set to expire after 24 hours.

---

## Tech Stack

-   [cite_start]**Backend**: **Go** 
    -   [cite_start]**Routing**: `github.com/gorilla/mux` v1.8.1 [cite: 1, 2]
    -   [cite_start]**WebSockets**: `github.com/gorilla/websocket` v1.5.3 [cite: 1, 2]
    -   [cite_start]**Redis Client**: `github.com/go-redis/redis/v8` v8.11.5 [cite: 1, 2]
-   **Database / Cache**: **Redis**
    -   Stores poll questions, options, and vote counts in Hashes.
    -   Uses Sets to track clients who have already voted.
    -   Acts as a message broker with its Pub/Sub feature for broadcasting updates.
-   **Frontend**: **Vanilla HTML5, CSS3, JavaScript** (No frameworks)

---

## Getting Started

Follow these instructions to get a local copy up and running.

### Prerequisites

Make sure you have the following installed:
-   [cite_start]**Go**: Version 1.23.6 or newer.
-   **Redis**: An active Redis server instance. The application is configured to connect to `localhost:6379` by default.

### Installation & Setup

1.  **Clone the repository:**
    ```sh
    git clone [https://github.com/kaushalvala/Pulse-Live-Voting.git](https://github.com/kaushalvala/Pulse-Live-Voting.git)
    ```

2.  **Navigate to the project directory:**
    ```sh
    cd Pulse-Live-Voting
    ```

3.  **Install Go dependencies:**
    [cite_start]The Go module system will typically handle this automatically based on the `go.mod` file. You can run this command to be sure.
    ```sh
    go mod tidy
    ```

4.  **Run the server:**
    ```sh
    go run main.go
    ```
    You should see a confirmation message in your terminal:
    ```
    Connected to Redis
    Server starting on :8080
    ```

5.  **Open the application:**
    Open your web browser and navigate to `http://localhost:8080`. You will be served the `index.html` file to create your first poll.

---

## How It Works

The application is divided into a Go backend and a vanilla JavaScript frontend that communicate via a REST API for initial data and WebSockets for real-time updates.

### Backend (Go)

1.  **Poll Creation (`POST /api/poll`)**:
    -   Receives a JSON object with a question and options.
    -   Generates a unique 6-character poll ID.
    -   Stores the poll data in a **Redis Hash** with a key like `poll:<pollID>`.
    -   Creates an empty **Redis Set** with a key like `voted:<pollID>` to track clients who have voted.
    -   Both the hash and the set are set to expire after 24 hours.

2.  **Serving Poll Data (`GET /api/poll/{pollID}`)**:
    -   Retrieves the poll data from the corresponding Redis hash and returns it as JSON.

3.  **Real-Time Communication (`/ws/{pollID}`)**:
    -   When a client connects, the HTTP connection is upgraded to a WebSocket.
    -   The server listens for incoming `vote` messages.
    -   When a vote is received, the server checks the `voted:<pollID>` set to see if the `clientID` has already voted.
    -   If not, it atomically increments the vote count in the Redis hash and adds the `clientID` to the voted set.
    -   It then publishes an `update` message to a Redis Pub/Sub channel named `updates:<pollID>`.
    -   A dedicated goroutine listens to all `updates:*` channels and broadcasts the payload to all WebSocket clients for that specific poll.

### Frontend (JavaScript)

1.  **Creation Page (`index.html`)**:
    -   A form allows users to input a question and dynamically add/remove options.
    -   On submission, it sends a `POST` request to `/api/poll` and displays the shareable poll link upon success.

2.  **Voting Page (`poll.html`)**:
    -   Extracts the poll ID from the URL.
    -   Generates or retrieves a unique `clientID` from `localStorage`.
    -   Fetches initial poll data from the `/api/poll/{pollID}` endpoint.
    -   Establishes a WebSocket connection to `/ws/{pollID}`.
    -   When the user votes, a JSON message is sent through the WebSocket, and the UI is locked.
    -   Listens for `voteUpdate` messages from the WebSocket to update the result bars and vote counts in real-time.

---

