// PicoClaw Web UI Application
class PicoClawApp {
    constructor() {
        this.messages = [];
        this.isStreaming = false;
        this.abortController = null;
        this.sessionKey = 'webui:' + Date.now();
        
        this.initElements();
        this.attachEventListeners();
        this.loadModels();
    }

    initElements() {
        this.elements = {
            chat: document.getElementById('chat'),
            userInput: document.getElementById('userInput'),
            sendBtn: document.getElementById('sendBtn'),
            provider: document.getElementById('provider'),
            model: document.getElementById('model'),
            streaming: document.getElementById('streaming'),
            clearBtn: document.getElementById('clearBtn'),
            statusText: document.getElementById('statusText'),
            providerStatus: document.getElementById('providerStatus'),
            systemPrompt: document.getElementById('systemPrompt'),
            systemPromptBtn: document.getElementById('systemPromptBtn')
        };
    }

    attachEventListeners() {
        this.elements.sendBtn.addEventListener('click', () => this.sendMessage());
        this.elements.userInput.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                this.sendMessage();
            }
        });
        this.elements.clearBtn.addEventListener('click', () => this.clearChat());
        this.elements.provider.addEventListener('change', () => this.loadModels());
        this.elements.systemPromptBtn.addEventListener('click', () => {
            this.elements.systemPrompt.classList.toggle('show');
        });
    }

    async loadModels() {
        const provider = this.elements.provider.value;
        this.updateStatus(`Loading models for ${provider}...`);
        
        try {
            const response = await fetch(`/api/models?provider=${provider}`);
            const data = await response.json();
            
            this.elements.model.innerHTML = '';
            
            if (Array.isArray(data) && data.length > 0) {
                data.forEach(providerData => {
                    providerData.models.forEach(model => {
                        const option = document.createElement('option');
                        option.value = model;
                        option.textContent = model;
                        this.elements.model.appendChild(option);
                    });
                });
            } else if (data.models) {
                data.models.forEach(model => {
                    const option = document.createElement('option');
                    option.value = model;
                    option.textContent = model;
                    this.elements.model.appendChild(option);
                });
            } else {
                const option = document.createElement('option');
                option.value = 'default';
                option.textContent = 'Default';
                this.elements.model.appendChild(option);
            }
            
            this.updateStatus(`Ready - ${provider} provider loaded`);
            this.updateProviderStatus(provider);
        } catch (error) {
            console.error('Failed to load models:', error);
            this.elements.model.innerHTML = '<option value="default">Default</option>';
            this.updateStatus(`Using ${provider} (offline mode)`);
            this.updateProviderStatus(provider);
        }
    }

    async sendMessage() {
        const content = this.elements.userInput.value.trim();
        if (!content || this.isStreaming) return;

        // Add user message
        this.addMessage('user', content);
        this.elements.userInput.value = '';
        this.elements.userInput.style.height = 'auto';

        // Prepare request
        const provider = this.elements.provider.value;
        const model = this.elements.model.value;
        const systemPrompt = this.elements.systemPrompt.value.trim();

        this.isStreaming = true;
        this.abortController = new AbortController();
        
        // Add assistant message placeholder
        const assistantMsgDiv = this.addMessage('assistant', '');
        const contentDiv = assistantMsgDiv.querySelector('.message-content');
        
        // Show typing indicator
        const typingIndicator = this.showTypingIndicator(contentDiv);

        try {
            const useStreaming = this.elements.streaming.checked;
            
            if (useStreaming) {
                await this.streamResponse(provider, model, content, systemPrompt, contentDiv, typingIndicator);
            } else {
                await this.nonStreamResponse(provider, model, content, systemPrompt, contentDiv, typingIndicator);
            }
        } catch (error) {
            contentDiv.innerHTML = `<span class="error">Error: ${error.message}</span>`;
        } finally {
            this.isStreaming = false;
            this.abortController = null;
            this.updateStatus('Ready');
        }
    }

    async streamResponse(provider, model, content, systemPrompt, contentDiv, typingIndicator) {
        this.updateStatus('Streaming response...');
        
        const response = await fetch('/api/chat', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                messages: [...this.messages, { role: 'user', content }],
                provider,
                model,
                systemPrompt,
                sessionKey: this.sessionKey
            }),
            signal: this.abortController.signal
        });

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        // Remove typing indicator
        if (typingIndicator) {
            typingIndicator.remove();
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let fullResponse = '';

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop() || '';

            for (const line of lines) {
                if (line.startsWith('data: ')) {
                    try {
                        const data = JSON.parse(line.slice(6));
                        
                        if (data.error) {
                            throw new Error(data.error);
                        }
                        
                        if (data.content) {
                            fullResponse += data.content;
                            contentDiv.innerHTML = this.formatMarkdown(fullResponse);
                            this.scrollToBottom();
                        }
                        
                        if (data.done) {
                            this.messages.push({ role: 'assistant', content: fullResponse });
                        }
                    } catch (e) {
                        console.error('Parse error:', e);
                    }
                }
            }
        }

        if (fullResponse) {
            this.messages.push({ role: 'assistant', content: fullResponse });
        }
    }

    async nonStreamResponse(provider, model, content, systemPrompt, contentDiv, typingIndicator) {
        this.updateStatus('Generating response...');
        
        // For non-streaming, we'll collect all chunks and display at once
        const response = await fetch('/api/chat', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                messages: [...this.messages, { role: 'user', content }],
                provider,
                model,
                systemPrompt,
                sessionKey: this.sessionKey
            })
        });

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let fullResponse = '';

        while ( true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop() || '';

            for (const line of lines) {
                if (line.startsWith('data: ')) {
                    try {
                        const data = JSON.parse(line.slice(6));
                        
                        if (data.error) {
                            throw new Error(data.error);
                        }
                        
                        if (data.content) {
                            fullResponse += data.content;
                        }
                        
                        if (data.done) {
                            if (typingIndicator) {
                                typingIndicator.remove();
                            }
                            contentDiv.innerHTML = this.formatMarkdown(fullResponse);
                            this.scrollToBottom();
                            this.messages.push({ role: 'assistant', content: fullResponse });
                        }
                    } catch (e) {
                        console.error('Parse error:', e);
                    }
                }
            }
        }

        if (fullResponse && !this.messages.find(m => m.content === fullResponse)) {
            this.messages.push({ role: 'assistant', content: fullResponse });
        }
    }

    addMessage(role, content) {
        // Remove welcome message if exists
        const welcome = this.elements.chat.querySelector('.welcome-message');
        if (welcome) {
            welcome.remove();
        }

        const messageDiv = document.createElement('div');
        messageDiv.className = `message ${role}`;
        
        const contentDiv = document.createElement('div');
        contentDiv.className = 'message-content';
        contentDiv.innerHTML = this.formatMarkdown(content);
        
        const iconDiv = document.createElement('div');
        iconDiv.className = 'message-icon';
        iconDiv.textContent = role === 'user' ? '👤' : '🦎';
        
        messageDiv.appendChild(iconDiv);
        messageDiv.appendChild(contentDiv);
        this.elements.chat.appendChild(messageDiv);
        
        this.scrollToBottom();
        
        return messageDiv;
    }

    showTypingIndicator(container) {
        const indicator = document.createElement('div');
        indicator.className = 'typing-indicator';
        indicator.innerHTML = '<span></span><span></span><span></span>';
        container.appendChild(indicator);
        return indicator;
    }

    formatMarkdown(text) {
        if (!text) return '';
        
        // Escape HTML
        let html = text
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;');
        
        // Code blocks
        html = html.replace(/```(\w+)?\n([\s\S]*?)```/g, (match, lang, code) => {
            return `<pre><code class="language-${lang || 'text'}">${code.trim()}</code></pre>`;
        });
        
        // Inline code
        html = html.replace(/`([^`]+)`/g, '<code>$1</code>');
        
        // Bold
        html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
        
        // Italic
        html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');
        
        // Line breaks
        html = html.replace(/\n/g, '<br>');
        
        return html;
    }

    clearChat() {
        if (confirm('Clear all messages?')) {
            this.messages = [];
            this.elements.chat.innerHTML = `
                <div class="welcome-message">
                    <h2>Welcome to PicoClaw! 🦎</h2>
                    <p>Your privacy-first AI assistant. All data stays local when using Ollama.</p>
                    <ul>
                        <li><strong>Ollama (Local):</strong> Runs on your machine - maximum privacy</li>
                        <li><strong>OpenRouter/OpenAI:</strong> Cloud APIs (requires API key)</li>
                    </ul>
                    <p>Start typing below to begin your conversation!</p>
                </div>
            `;
            this.updateStatus('Chat cleared');
        }
    }

    updateStatus(text) {
        this.elements.statusText.textContent = text;
    }

    updateProviderStatus(provider) {
        this.elements.providerStatus.textContent = `Connected to ${provider}`;
        this.elements.providerStatus.style.display = 'inline';
    }

    scrollToBottom() {
        this.elements.chat.scrollTop = this.elements.chat.scrollHeight;
    }
}

// Initialize app when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
    window.app = new PicoClawApp();
});
