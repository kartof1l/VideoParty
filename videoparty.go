package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Константы
const (
	MaxMessageSize = 1024
	PongWait       = 60 * time.Second
	PingPeriod     = (PongWait * 9) / 10
	WriteWait      = 10 * time.Second
)

// Структуры
type Room struct {
	ID        string
	Name      string
	VideoURL  string
	Owner     string
	CreatedAt time.Time
	clients   map[*Client]bool
	mu        sync.RWMutex
}

type Client struct {
	conn     *websocket.Conn
	room     *Room
	username string
	send     chan []byte
}

type Message struct {
	Type string      `json:"type"`
	User string      `json:"user,omitempty"`
	Data interface{} `json:"data,omitempty"`
	Time int64       `json:"time,omitempty"`
}

type VideoState struct {
	Playing      bool    `json:"playing"`
	CurrentTime  float64 `json:"currentTime"`
	PlaybackRate float64 `json:"playbackRate,omitempty"`
}

// Глобальные переменные
var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	rooms = struct {
		sync.RWMutex
		m map[string]*Room
	}{
		m: make(map[string]*Room),
	}
)

// Главная функция
func main() {
	// Маршруты
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/create-room", createRoomHandler)
	http.HandleFunc("/room/", roomHandler)
	http.HandleFunc("/ws/", websocketHandler)
	http.HandleFunc("/rooms", listRoomsHandler)

	log.Println("🚀 VideoParty with WebSocket starting on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Главная страница
func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	html := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>🎬 VideoParty - Watch Videos Together</title>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<style>
			* { margin: 0; padding: 0; box-sizing: border-box; }
			body {
				font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
				background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
				color: white;
				min-height: 100vh;
				padding: 20px;
			}
			.container {
				max-width: 600px;
				margin: 50px auto;
				background: rgba(255, 255, 255, 0.05);
				padding: 40px;
				border-radius: 20px;
				border: 1px solid rgba(255, 255, 255, 0.1);
			}
			h1 { text-align: center; margin-bottom: 30px; color: #00adb5; }
			.form-group { margin-bottom: 25px; }
			label { display: block; margin-bottom: 8px; font-weight: 600; color: #00adb5; }
			input {
				width: 100%; padding: 14px;
				border: 2px solid #393e46; border-radius: 8px;
				background: rgba(255, 255, 255, 0.1);
				color: white; font-size: 16px;
			}
			input:focus { outline: none; border-color: #00adb5; }
			.btn {
				width: 100%; padding: 16px;
				background: linear-gradient(45deg, #00adb5, #0097a7);
				color: white; border: none; border-radius: 8px;
				font-size: 18px; font-weight: 600; cursor: pointer;
				margin-top: 10px;
			}
			.btn:hover { background: linear-gradient(45deg, #0097a7, #00838f); }
			.error {
				color: #ff6b6b; background: rgba(255, 107, 107, 0.1);
				padding: 10px; border-radius: 5px; margin: 10px 0;
				border: 1px solid #ff6b6b;
			}
			.rooms-link {
				display: block; text-align: center; margin-top: 20px;
				color: #00adb5; text-decoration: none;
			}
		</style>
	</head>
	<body>
		<div class="container">
			<h1>🎬 VideoParty</h1>
			<p style="text-align: center; margin-bottom: 30px; color: #aaa;">
				Watch videos together with friends in real-time
			</p>
			
			<form action="/create-room" method="POST">
				<div class="form-group">
					<label for="videoUrl">🎥 Video URL</label>
					<input type="url" id="videoUrl" name="videoUrl" 
						   placeholder="https://www.youtube.com/watch?v=..." 
						   required autofocus>
				</div>
				
				<div class="form-group">
					<label for="roomName">🚪 Room Name (optional)</label>
					<input type="text" id="roomName" name="roomName" 
						   placeholder="Movie Night with Friends">
				</div>
				
				<div class="form-group">
					<label for="username">👤 Your Name</label>
					<input type="text" id="username" name="username" 
						   placeholder="Enter your name" required>
				</div>
				
				<button type="submit" class="btn">🎬 Create Room & Start Watching</button>
			</form>
			
			<a href="/rooms" class="rooms-link">👥 View existing rooms</a>
		</div>
		
		<script>
			document.querySelector('form').addEventListener('submit', function(e) {
				const url = document.getElementById('videoUrl').value;
				if (!url) {
					e.preventDefault();
					alert('Please enter a video URL');
					return;
				}
				if (!url.startsWith('http')) {
					e.preventDefault();
					alert('Please enter a valid URL (start with http:// or https://)');
				}
			});
		</script>
	</body>
	</html>
	`

	errorMsg := r.URL.Query().Get("error")
	if errorMsg != "" {
		html = strings.Replace(html, "</form>",
			`<div class="error">❌ `+errorMsg+`</div></form>`, 1)
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

// Создание комнаты
func createRoomHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseForm()
	videoURL := r.FormValue("videoUrl")
	roomName := r.FormValue("roomName")
	username := r.FormValue("username")

	if videoURL == "" || username == "" {
		http.Redirect(w, r, "/?error=Video+URL+and+username+are+required", http.StatusSeeOther)
		return
	}

	roomID := generateRoomID()
	if roomName == "" {
		roomName = "Room " + roomID[:4]
	}

	room := &Room{
		ID:        roomID,
		Name:      roomName,
		VideoURL:  videoURL,
		Owner:     username,
		CreatedAt: time.Now(),
		clients:   make(map[*Client]bool),
	}

	rooms.Lock()
	rooms.m[roomID] = room
	rooms.Unlock()

	log.Printf("🎬 Room created: %s - %s by %s", roomID, roomName, username)
	http.Redirect(w, r, "/room/"+roomID+"?username="+username, http.StatusSeeOther)
}

// Страница комнаты
func roomHandler(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.NotFound(w, r)
		return
	}
	roomID := pathParts[2]

	rooms.RLock()
	room, exists := rooms.m[roomID]
	rooms.RUnlock()

	if !exists {
		http.NotFound(w, r)
		return
	}

	username := r.URL.Query().Get("username")
	if username == "" {
		username = "Guest_" + generateRoomID()[:4]
	}

	// Список пользователей
	room.mu.RLock()
	userCount := len(room.clients)
	room.mu.RUnlock()

	embedHTML := generateVideoEmbed(room.VideoURL)

	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
	<title>🎬 %s - VideoParty</title>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	%s
	<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css">
</head>
<body>
	<!-- Навигация (как на главной) -->
	<nav class="navbar">
		<div class="nav-brand">
			<i class="fas fa-video"></i>
			<h1>VideoParty</h1>
		</div>
		<div class="nav-links">
			<a href="/"><i class="fas fa-home"></i> Home</a>
			<a href="/rooms"><i class="fas fa-users"></i> Rooms</a>
			<a href="#" onclick="showHelp()"><i class="fas fa-question-circle"></i> Help</a>
		</div>
	</nav>

	<main class="container">
		<!-- Герой-секция (как на главной) -->
		<div class="hero">
			<div class="hero-content">
				<h2><i class="fas fa-film"></i> %s</h2>
				<p class="subtitle">Watching together in real-time</p>
				
				<div class="room-info">
					<p><i class="fas fa-user"></i> Host: <strong>%s</strong></p>
					<p><i class="fas fa-hashtag"></i> Room ID: <span class="room-id">%s</span></p>
					<p><i class="fas fa-users"></i> <span id="userCount">%d</span> users watching</p>
				</div>
				
				<!-- Инвайт секция -->
				<div class="invite-section">
					<h3><i class="fas fa-user-plus"></i> Invite Friends</h3>
					<div class="invite-link">
						<input type="text" id="inviteInput" value="https://videoparty-1.onrender.com/room/%s" readonly>
								<button class="btn btn-primary" onclick="copyInviteLink()">
							<i class="fas fa-copy"></i> Copy Link
						</button>
					</div>
					<div id="copyNotification" class="notification">Link copied to clipboard!</div>
				</div>
				
				<!-- Видео плеер -->
				<div class="video-container">
					<h3><i class="fas fa-play-circle"></i> Now Playing</h3>
					%s
					
					<div class="controls">
						<button class="btn btn-primary" id="syncBtn" onclick="syncWithRoom()">
							<i class="fas fa-sync-alt"></i> Sync with Room
						</button>
						<button class="btn btn-secondary" onclick="openOriginal()">
							<i class="fas fa-external-link-alt"></i> Open Original
						</button>
						<button class="btn btn-danger" onclick="leaveRoom()">
							<i class="fas fa-sign-out-alt"></i> Leave Room
						</button>
					</div>
				</div>
				
				<!-- Пользователи -->
				<div class="user-list">
					<h3><i class="fas fa-users"></i> Users in Room</h3>
					<div id="usersList">
						<span class="user-badge owner">%s <i class="fas fa-crown"></i></span>
					</div>
				</div>
				
				<!-- Чат -->
				<div class="chat-section">
					<h3><i class="fas fa-comments"></i> Live Chat</h3>
					<div class="chat-messages" id="chatMessages"></div>
					<div class="chat-input">
						<input type="text" id="chatInput" placeholder="Type a message..." 
							   onkeypress="if(event.key=='Enter') sendMessage()">
						<button class="btn btn-primary" onclick="sendMessage()">
							<i class="fas fa-paper-plane"></i> Send
						</button>
					</div>
				</div>
				
				<!-- Статус -->
				<div class="connection-status">
					<span id="status"><i class="fas fa-plug"></i> Connecting...</span>
				</div>
				
				<!-- Кнопка назад -->
				<a href="/" class="back-link">
					<i class="fas fa-arrow-left"></i> Back to Home
				</a>
			</div>
		</div>
		
		<!-- Платформы (как на главной) -->
		<div class="platforms">
			<h3><i class="fas fa-check-circle"></i> Supported Platforms</h3>
			<div class="platform-icons">
				<div class="platform">
					<i class="fab fa-youtube"></i>
					<span>YouTube</span>
				</div>
				<div class="platform">
					<i class="fab fa-vimeo-v"></i>
					<span>Vimeo</span>
				</div>
				<div class="platform">
					<i class="fas fa-video"></i>
					<span>Direct Videos</span>
				</div>
				<div class="platform">
					<i class="fas fa-link"></i>
					<span>External Links</span>
				</div>
			</div>
		</div>
	</main>

	<!-- Футер (как на главной) -->
	<footer>
		<p>Watch videos together • Made with Go & <i class="fas fa-heart" style="color: #ff6b6b;"></i></p>
	</footer>

	<script>
		// WebSocket и управление видео
		%s
	</script>
</body>
</html>
`,
		room.Name,
		// Стили
		`<style>`+getCSS()+`</style>`,
		// Контент
		room.Name,
		room.Owner,
		roomID,
		userCount,
		r.Host,
		roomID,
		embedHTML,
		room.Owner,
		// JavaScript
		getRoomJavaScript(roomID, username, room.VideoURL, room.Owner))

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

// JavaScript для комнаты
func getRoomJavaScript(roomID, username, videoURL, owner string) string {
	return fmt.Sprintf(`
const roomId = "%s";
const username = "%s";
const videoUrl = "%s";
const ownerName = "%s";
let ws;

// WebSocket соединение
function connectWebSocket() {
	const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
	ws = new WebSocket(protocol + '//' + window.location.host + '/ws/' + roomId + '?username=' + encodeURIComponent(username));
	
	ws.onopen = function() {
		console.log('WebSocket connected');
		updateStatus('<i class="fas fa-check-circle"></i> Connected');
		ws.send(JSON.stringify({type: 'join', user: username}));
	};
	
	ws.onmessage = function(event) {
		const msg = JSON.parse(event.data);
		handleMessage(msg);
	};
	
	ws.onclose = function() {
		updateStatus('<i class="fas fa-times-circle"></i> Disconnected - Reconnecting...');
		setTimeout(connectWebSocket, 3000);
	};
	
	ws.onerror = function(error) {
		console.error('WebSocket error:', error);
		updateStatus('<i class="fas fa-exclamation-triangle"></i> Connection error');
	};
}

function handleMessage(msg) {
	switch(msg.type) {
		case 'chat':
			addChatMessage(msg.user, msg.data);
			break;
		
		case 'users':
			updateUsersList(msg.data);
			break;
		
		case 'play':
			playVideo();
			break;
		
		case 'pause':
			pauseVideo();
			break;
		
		case 'seek':
			seekVideo(msg.data);
			break;
		
		case 'state':
			syncVideo(msg.data);
			break;
	}
}

function updateUsersList(users) {
	const list = document.getElementById('usersList');
	list.innerHTML = '';
	users.forEach(user => {
		const badge = document.createElement('span');
		badge.className = 'user-badge' + (user === ownerName ? ' owner' : '');
		badge.innerHTML = user + (user === ownerName ? ' <i class="fas fa-crown"></i>' : '');
		list.appendChild(badge);
	});
	document.getElementById('userCount').textContent = users.length;
}

function addChatMessage(user, text) {
	const chat = document.getElementById('chatMessages');
	const msgDiv = document.createElement('div');
	msgDiv.className = 'chat-message';
	msgDiv.innerHTML = '<strong>' + user + ':</strong> ' + text;
	chat.appendChild(msgDiv);
	chat.scrollTop = chat.scrollHeight;
}

// Управление видео
function playVideo() {
	const video = document.querySelector('video');
	if (video) video.play();
}

function pauseVideo() {
	const video = document.querySelector('video');
	if (video) video.pause();
}

function seekVideo(time) {
	const video = document.querySelector('video');
	if (video) video.currentTime = time;
}

function syncVideo(state) {
	if (state.currentTime) seekVideo(state.currentTime);
	if (state.playing) playVideo(); else pauseVideo();
}

function sendMessage() {
	const input = document.getElementById('chatInput');
	const text = input.value.trim();
	if (text && ws.readyState === WebSocket.OPEN) {
		ws.send(JSON.stringify({type: 'chat', user: username, data: text}));
		input.value = '';
	}
}

function syncWithRoom() {
	if (ws.readyState === WebSocket.OPEN) {
		const video = document.querySelector('video');
		if (video) {
			ws.send(JSON.stringify({
				type: 'state_update',
				user: username,
				data: {
					playing: !video.paused,
					currentTime: video.currentTime
				}
			}));
		}
	}
}

function copyInviteLink() {
	const input = document.getElementById('inviteInput');
	input.select();
	navigator.clipboard.writeText(input.value);
	
	const notification = document.getElementById('copyNotification');
	notification.style.display = 'block';
	notification.innerHTML = '<i class="fas fa-check"></i> Link copied to clipboard!';
	setTimeout(() => {
		notification.style.display = 'none';
	}, 2000);
}

function openOriginal() {
	window.open(videoUrl, '_blank');
}

function leaveRoom() {
	if (confirm('Leave this room?')) {
		if (ws.readyState === WebSocket.OPEN) {
			ws.send(JSON.stringify({type: 'leave', user: username}));
			ws.close();
		}
		window.location.href = '/';
	}
}

function updateStatus(text) {
	document.getElementById('status').innerHTML = text;
}

function showHelp() {
	alert('🎬 VideoParty Help:\\n\\n' +
		  '1. Share the invite link with friends\\n' +
		  '2. Use "Sync with Room" to match playback\\n' +
		  '3. Chat with others in real-time\\n' +
		  '4. Play/pause/seek will sync with everyone');
}

// Отслеживание видео событий
function setupVideoListeners() {
	const video = document.querySelector('video');
	if (video) {
		video.addEventListener('play', function() {
			if (ws.readyState === WebSocket.OPEN) {
				ws.send(JSON.stringify({type: 'play', user: username}));
			}
		});
		
		video.addEventListener('pause', function() {
			if (ws.readyState === WebSocket.OPEN) {
				ws.send(JSON.stringify({type: 'pause', user: username}));
			}
		});
		
		video.addEventListener('seeked', function() {
			if (ws.readyState === WebSocket.OPEN) {
				ws.send(JSON.stringify({
					type: 'seek',
					user: username,
					data: video.currentTime
				}));
			}
		});
	}
}

// Запуск
window.onload = function() {
	connectWebSocket();
	setupVideoListeners();
	// Авто-фокус на чате
	document.getElementById('chatInput').focus();
};
`, roomID, username, videoURL, owner)
}

// WebSocket handler
func websocketHandler(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.NotFound(w, r)
		return
	}
	roomID := pathParts[2]

	username := r.URL.Query().Get("username")
	if username == "" {
		username = "Guest_" + generateRoomID()[:4]
	}

	rooms.RLock()
	room, exists := rooms.m[roomID]
	rooms.RUnlock()

	if !exists {
		http.NotFound(w, r)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &Client{
		conn:     conn,
		room:     room,
		username: username,
		send:     make(chan []byte, 256),
	}

	room.mu.Lock()
	room.clients[client] = true
	room.mu.Unlock()

	log.Printf("👤 User '%s' joined room '%s'", username, roomID)

	go client.writePump()
	go client.readPump()

	// Отправляем список пользователей всем
	client.broadcastUsers()
}

func (c *Client) readPump() {
	defer c.disconnect()

	c.conn.SetReadLimit(MaxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(PongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(PongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Error parsing message: %v", err)
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *Client) handleMessage(msg Message) {
	switch msg.Type {
	case "chat":
		c.broadcastMessage(Message{
			Type: "chat",
			User: c.username,
			Data: msg.Data,
			Time: time.Now().Unix(),
		})

	case "play":
		c.broadcastMessage(Message{
			Type: "play",
			User: c.username,
			Time: time.Now().Unix(),
		})

	case "pause":
		c.broadcastMessage(Message{
			Type: "pause",
			User: c.username,
			Time: time.Now().Unix(),
		})

	case "seek":
		c.broadcastMessage(Message{
			Type: "seek",
			User: c.username,
			Data: msg.Data,
			Time: time.Now().Unix(),
		})

	case "state_update":
		c.broadcastMessage(Message{
			Type: "state",
			Data: msg.Data,
			Time: time.Now().Unix(),
		})

	case "join":
		c.broadcastUsers()

	case "leave":
		c.disconnect()
	}
}

func (c *Client) broadcastMessage(msg Message) {
	data, _ := json.Marshal(msg)

	c.room.mu.RLock()
	defer c.room.mu.RUnlock()

	for client := range c.room.clients {
		if client != c {
			select {
			case client.send <- data:
			default:
				close(client.send)
				delete(c.room.clients, client)
			}
		}
	}
}

func (c *Client) broadcastUsers() {
	users := c.getUsersList()
	msg := Message{
		Type: "users",
		Data: users,
		Time: time.Now().Unix(),
	}
	c.broadcastMessage(msg)
}

func (c *Client) getUsersList() []string {
	c.room.mu.RLock()
	defer c.room.mu.RUnlock()

	users := make([]string, 0, len(c.room.clients))
	for client := range c.room.clients {
		users = append(users, client.username)
	}
	return users
}

func (c *Client) writePump() {
	ticker := time.NewTicker(PingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) disconnect() {
	c.room.mu.Lock()
	if _, ok := c.room.clients[c]; ok {
		delete(c.room.clients, c)
		close(c.send)
		log.Printf("👋 User '%s' left room '%s'", c.username, c.room.ID)
	}
	c.room.mu.Unlock()

	c.conn.Close()
	c.broadcastUsers()
}

// Список комнат
func listRoomsHandler(w http.ResponseWriter, r *http.Request) {
	rooms.RLock()
	defer rooms.RUnlock()

	html := `<html><head><title>Active Rooms</title>
	<style>
		body { font-family: Arial; padding: 20px; background: #f5f5f5; }
		.container { max-width: 800px; margin: 0 auto; }
		h1 { color: #333; }
		.room { background: white; padding: 15px; margin: 10px 0; border-radius: 5px; box-shadow: 0 2px 5px rgba(0,0,0,0.1); }
		.room a { color: #2196f3; text-decoration: none; font-weight: bold; }
		.room a:hover { text-decoration: underline; }
	</style>
	</head>
	<body><div class="container"><h1>🎬 Active Rooms</h1>`

	if len(rooms.m) == 0 {
		html += `<p>No active rooms. <a href="/">Create one!</a></p>`
	} else {
		for id, room := range rooms.m {
			room.mu.RLock()
			userCount := len(room.clients)
			room.mu.RUnlock()
			html += fmt.Sprintf(`
			<div class="room">
				<a href="/room/%s">%s</a>
				<p>Host: %s | 👥 %d users | Created: %s</p>
				<small>ID: %s</small>
			</div>
			`, id, room.Name, room.Owner, userCount, room.CreatedAt.Format("15:04"), id)
		}
	}

	html += `<p><a href="/">← Back to Home</a></p></div></body></html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

// Генерация embed кода видео
func generateVideoEmbed(videoURL string) string {
	// YouTube
	if strings.Contains(videoURL, "youtube.com") || strings.Contains(videoURL, "youtu.be") {
		var videoID string
		if strings.Contains(videoURL, "v=") {
			videoID = strings.Split(videoURL, "v=")[1]
			if len(videoID) > 11 {
				videoID = videoID[:11]
			}
		} else if strings.Contains(videoURL, "youtu.be/") {
			videoID = strings.Split(videoURL, "youtu.be/")[1]
			if len(videoID) > 11 {
				videoID = videoID[:11]
			}
		}

		if videoID != "" {
			return fmt.Sprintf(`
			<div class="video-wrapper">
				<iframe 
					src="https://www.youtube.com/embed/%s" 
					frameborder="0" 
					allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture" 
					allowfullscreen>
				</iframe>
			</div>
			`, videoID)
		}
	}

	// Direct video files
	if strings.Contains(videoURL, ".mp4") ||
		strings.Contains(videoURL, ".webm") ||
		strings.Contains(videoURL, ".mov") ||
		strings.Contains(videoURL, ".avi") {
		return fmt.Sprintf(`
		<div class="video-wrapper">
			<video controls style="width:100%%; height:100%%;">
				<source src="%s" type="video/mp4">
				Your browser does not support the video tag.
			</video>
		</div>
		`, videoURL)
	}

	// Для других сервисов
	return fmt.Sprintf(`
	<div class="external-video">
		<p>🎥 <a href="%s" target="_blank">Open video in new tab</a></p>
	</div>
	`, videoURL)
}

// CSS стили
func getCSS() string {
	return `
	* {
		margin: 0;
		padding: 0;
		box-sizing: border-box;
	}
	
	body {
		font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
		background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
		color: white;
		min-height: 100vh;
		display: flex;
		flex-direction: column;
	}
	
	/* Навигация */
	.navbar {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 1rem 2rem;
		background: rgba(0, 0, 0, 0.7);
		backdrop-filter: blur(10px);
		border-bottom: 1px solid #00adb5;
	}
	
	.nav-brand {
		display: flex;
		align-items: center;
		gap: 10px;
	}
	
	.nav-brand i {
		font-size: 2rem;
		color: #00adb5;
	}
	
	.nav-links {
		display: flex;
		gap: 2rem;
	}
	
	.nav-links a {
		color: #fff;
		text-decoration: none;
		transition: color 0.3s;
		display: flex;
		align-items: center;
		gap: 5px;
	}
	
	.nav-links a:hover {
		color: #00adb5;
	}
	
	/* Контейнеры */
	.container {
		max-width: 1200px;
		margin: 0 auto;
		padding: 20px;
		flex: 1;
	}
	
	/* Герой-секция (как на главной) */
	.hero {
		background: rgba(255, 255, 255, 0.05);
		border-radius: 20px;
		padding: 3rem;
		margin: 2rem 0;
		backdrop-filter: blur(10px);
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.hero h2 {
		font-size: 2.5rem;
		margin-bottom: 1rem;
		color: #00adb5;
	}
	
	.subtitle {
		font-size: 1.2rem;
		color: #aaa;
		margin-bottom: 2rem;
	}
	
	/* Формы и инпуты (как на главной) */
	.input-group {
		margin-bottom: 1.5rem;
	}
	
	.input-group label {
		display: block;
		margin-bottom: 0.5rem;
		color: #00adb5;
		font-weight: 600;
	}
	
	.input-group input {
		width: 100%;
		padding: 12px 15px;
		border: 2px solid #393e46;
		border-radius: 8px;
		background: rgba(255, 255, 255, 0.1);
		color: white;
		font-size: 1rem;
		transition: border-color 0.3s;
	}
	
	.input-group input:focus {
		outline: none;
		border-color: #00adb5;
		box-shadow: 0 0 0 2px rgba(0, 173, 181, 0.2);
	}
	
	.input-hint {
		margin-top: 0.5rem;
		color: #888;
		font-size: 0.9rem;
	}
	
	/* Кнопки (как на главной) */
	.button-group {
		display: flex;
		gap: 1rem;
		margin-top: 2rem;
	}
	
	.btn {
		padding: 12px 24px;
		border: none;
		border-radius: 8px;
		font-size: 1rem;
		font-weight: 600;
		cursor: pointer;
		transition: all 0.3s;
		display: inline-flex;
		align-items: center;
		gap: 8px;
	}
	
	.btn-primary {
		background: linear-gradient(45deg, #00adb5, #0097a7);
		color: white;
	}
	
	.btn-primary:hover {
		background: linear-gradient(45deg, #0097a7, #00838f);
		transform: translateY(-2px);
		box-shadow: 0 5px 15px rgba(0, 173, 181, 0.4);
	}
	
	.btn-secondary {
		background: rgba(255, 255, 255, 0.1);
		color: white;
		border: 2px solid #00adb5;
	}
	
	.btn-secondary:hover {
		background: rgba(0, 173, 181, 0.1);
	}
	
	.btn-danger {
		background: linear-gradient(45deg, #ff416c, #ff4b2b);
	}
	
	/* Видео-контейнер */
	.video-container {
		background: rgba(0, 0, 0, 0.3);
		border-radius: 15px;
		padding: 2rem;
		margin: 2rem 0;
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.video-wrapper {
		position: relative;
		padding-bottom: 56.25%; /* 16:9 Aspect Ratio */
		height: 0;
		overflow: hidden;
		border-radius: 10px;
		background: #000;
		margin-bottom: 20px;
	}
	
	.video-wrapper iframe,
	.video-wrapper video {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: 100%;
		border: none;
	}
	
	/* Контролы */
	.controls {
		display: flex;
		gap: 1rem;
		margin-top: 1.5rem;
	}
	
	/* Список пользователей */
	.user-list {
		background: rgba(0, 0, 0, 0.3);
		padding: 1.5rem;
		border-radius: 10px;
		margin: 1.5rem 0;
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.user-badge {
		display: inline-block;
		background: rgba(0, 173, 181, 0.2);
		padding: 8px 16px;
		border-radius: 20px;
		margin: 5px;
		border: 1px solid #00adb5;
	}
	
	.user-badge.owner {
		background: rgba(255, 193, 7, 0.2);
		border-color: #ffc107;
	}
	
	/* Чат */
	.chat-section {
		background: rgba(0, 0, 0, 0.3);
		padding: 1.5rem;
		border-radius: 10px;
		margin: 1.5rem 0;
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.chat-messages {
		height: 200px;
		overflow-y: auto;
		padding: 10px;
		background: rgba(0, 0, 0, 0.5);
		border-radius: 8px;
		margin-bottom: 10px;
	}
	
	.chat-message {
		margin-bottom: 10px;
		padding: 10px;
		background: rgba(255, 255, 255, 0.05);
		border-radius: 8px;
	}
	
	.chat-input {
		display: flex;
		gap: 10px;
	}
	
	.chat-input input {
		flex: 1;
		padding: 12px;
		border: 2px solid #00adb5;
		border-radius: 8px;
		background: rgba(255, 255, 255, 0.1);
		color: white;
		font-size: 1rem;
	}
	
	/* Инвайт секция */
	.invite-section {
		background: rgba(0, 173, 181, 0.1);
		padding: 1.5rem;
		border-radius: 10px;
		margin: 1.5rem 0;
		border: 1px solid #00adb5;
	}
	
	.invite-link {
		display: flex;
		gap: 10px;
		margin: 10px 0;
	}
	
	.invite-link input {
		flex: 1;
		padding: 12px;
		border: 2px solid #00adb5;
		border-radius: 8px;
		background: rgba(255, 255, 255, 0.1);
		color: white;
		font-size: 1rem;
	}
	
	/* Уведомления */
	.notification {
		background: #4caf50;
		color: white;
		padding: 12px;
		border-radius: 8px;
		margin: 10px 0;
		display: none;
	}
	
	/* Статус подключения */
	.connection-status {
		margin-top: 1.5rem;
		padding: 12px;
		background: rgba(0, 0, 0, 0.3);
		border-radius: 8px;
		text-align: center;
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	/* Футер */
	footer {
		text-align: center;
		padding: 2rem;
		background: rgba(0, 0, 0, 0.7);
		color: #888;
		margin-top: auto;
		border-top: 1px solid #00adb5;
	}
	
	/* Информация о комнате */
	.room-info {
		background: rgba(0, 0, 0, 0.3);
		padding: 1rem;
		border-radius: 8px;
		margin: 1rem 0;
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.room-id {
		background: rgba(0, 173, 181, 0.2);
		padding: 5px 10px;
		border-radius: 5px;
		font-family: monospace;
		color: #00adb5;
	}
	
	/* Ссылки */
	.back-link {
		display: inline-block;
		margin-top: 1.5rem;
		color: #00adb5;
		text-decoration: none;
		font-weight: 600;
	}
	
	.back-link:hover {
		text-decoration: underline;
	}
	
	/* Платформы (как на главной) */
	.platforms {
		margin-top: 3rem;
		padding-top: 2rem;
		border-top: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.platform-icons {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
		gap: 1rem;
		margin-top: 1rem;
	}
	
	.platform {
		background: rgba(255, 255, 255, 0.05);
		padding: 1rem;
		border-radius: 10px;
		display: flex;
		align-items: center;
		gap: 10px;
		transition: transform 0.3s;
	}
	
	.platform:hover {
		transform: translateY(-5px);
		background: rgba(255, 255, 255, 0.1);
	}
	
	.platform i {
		font-size: 1.5rem;
	}
	
	.platform i.fa-youtube { color: #ff0000; }
	.platform i.fa-vimeo-v { color: #1ab7ea; }
	.platform i.fa-video { color: #00adb5; }
	.platform i.fa-link { color: #9146ff; }
	
	/* Внешнее видео */
	.external-video {
		padding: 3rem;
		text-align: center;
		background: rgba(0, 0, 0, 0.3);
		border-radius: 15px;
		margin: 2rem 0;
		border: 1px solid rgba(255, 255, 255, 0.1);
	}
	
	.external-video a {
		color: #00adb5;
		text-decoration: none;
		font-weight: 600;
	}
	
	.external-video a:hover {
		text-decoration: underline;
	}
	
	/* Адаптивность */
	@media (max-width: 768px) {
		.container {
			padding: 15px;
		}
		
		.hero {
			padding: 2rem;
		}
		
		.navbar {
			flex-direction: column;
			gap: 1rem;
			padding: 1rem;
		}
		
		.nav-links {
			gap: 1rem;
		}
		
		.controls {
			flex-direction: column;
		}
		
		.button-group {
			flex-direction: column;
		}
		
		.invite-link {
			flex-direction: column;
		}
		
		.chat-input {
			flex-direction: column;
		}
	}
	`
}

// Генерация ID комнаты
func generateRoomID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:8]
}



