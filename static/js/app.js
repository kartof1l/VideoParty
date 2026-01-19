// Функция для присоединения к существующей комнате
function joinExistingRoom() {
    document.getElementById('joinModal').style.display = 'block';
}

function closeModal() {
    document.getElementById('joinModal').style.display = 'none';
}

// Закрытие модального окна при клике вне его
window.onclick = function(event) {
    const modal = document.getElementById('joinModal');
    if (event.target === modal) {
        closeModal();
    }
}

// Валидация URL перед отправкой
document.getElementById('videoForm').addEventListener('submit', function(e) {
    const urlInput = document.getElementById('videoUrl');
    const url = urlInput.value.trim();
    
    if (!url) {
        e.preventDefault();
        alert('Please enter a video URL');
        return;
    }
    
    // Простая валидация URL
    try {
        new URL(url);
    } catch (_) {
        e.preventDefault();
        alert('Please enter a valid URL (include http:// or https://)');
        urlInput.focus();
    }
});

// Пример: автоматическое определение платформы
document.getElementById('videoUrl').addEventListener('input', function(e) {
    const url = e.target.value;
    const platforms = document.querySelectorAll('.platform');
    
    // Сброс всех выделений
    platforms.forEach(p => p.style.opacity = '0.5');
    
    // Определяем платформу и выделяем её
    if (url.includes('youtube.com') || url.includes('youtu.be')) {
        highlightPlatform('YouTube');
    } else if (url.includes('vimeo.com')) {
        highlightPlatform('Vimeo');
    } else if (url.includes('twitch.tv')) {
        highlightPlatform('Twitch');
    } else if (url.includes('.mp4') || url.includes('.webm') || url.includes('.mov')) {
        highlightPlatform('Direct Links');
    }
});

function highlightPlatform(platformName) {
    const platforms = document.querySelectorAll('.platform');
    platforms.forEach(p => {
        if (p.querySelector('span').textContent === platformName) {
            p.style.opacity = '1';
            p.style.border = '2px solid #00adb5';
        } else {
            p.style.border = 'none';
        }
    });
}