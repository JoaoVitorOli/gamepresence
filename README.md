# Game Presence

App para Windows 11 que mostra no Discord (Rich Presence via IPC local) o jogo que você está jogando, com o nome e a capa que você escolher. Feito em Go com o [MyGo](https://github.com/egoist/mygo) (v0.5.5), com interface nativa desenhada pelo próprio MyGo (Direct3D 11), sem WebView2 e sem HTML. A interface do app é toda em inglês.

## Instalar / rodar

- Executável único, que roda sem instalar: `dist\windows-amd64\Game Presence.exe`
- Instalador (opcional): `dist\windows-amd64\Game Presence Setup 1.0.0 amd64.exe`

Fechar a janela não encerra o app: ele continua rodando na bandeja (ícone roxo perto do relógio). Clique no ícone para abrir a janela e clique com o botão direito para **Pause presence** / **Quit**. Abrir o .exe de novo só reabre a janela; não cria uma segunda cópia.

### Consumo (medido)
- **Na bandeja** (o normal enquanto você joga): ~3 MB de memória em uso, um único processo, CPU praticamente zero (verifica os processos a cada 5 s).
- **Com a janela aberta**: ~70–80 MB, ainda um único processo. A versão anterior, com WebView2, usava ~370 MB em 7 processos.
- Ao fechar, a janela é destruída e a memória é devolvida ao Windows.

## Configuração (uma vez, com a sua conta)

### 1. Discord Application ID (obrigatório)
1. Abra https://discord.com/developers/applications e clique em **New Application**.
2. O nome do Application é o que o Discord mostra no título do card de atividade. Use algo genérico, por exemplo "a game".
3. Em **General Information**, copie o **Application ID** e cole em **Settings → Application ID** no app.
4. No Discord, ative **Configurações de usuário → Privacidade de atividades → Compartilhar suas atividades detectadas**.

### 2. Busca de capas no IGDB (opcional)
1. Ative a autenticação em dois fatores na Twitch (é exigido) e abra https://dev.twitch.tv/console/apps → **Register Your Application**.
2. Nome à sua escolha, OAuth Redirect URL `http://localhost`, categoria **Application Integration**, tipo de cliente **Confidential**.
3. Copie o **Client ID**, gere um **Client Secret** (botão **New Secret**) e cole os dois em **Settings → IGDB cover search**.

### 3. Upload de imagens locais (opcional)
O Discord só mostra imagens a partir de um link público, então um arquivo local precisa ser enviado para um host antes. **A imagem enviada fica pública.**
- **Imgur**: registre um app em https://api.imgur.com/oauth2/addclient ("Anonymous usage without user authorization") e cole o **Client ID**. Dá para apagar a imagem depois.
- **catbox.moe**: não precisa de conta, mas a imagem não pode ser apagada depois.

As chaves ficam guardadas no **Gerenciador de Credenciais do Windows**, não no arquivo de configuração, e nunca vão para o log.

## Cadastrar o Bloodborne
1. Abra o jogo pelo `Jogar Bloodborne.bat`.
2. Clique em **Add game → Choose running…** e escolha `bb-probe.exe` (a janela tem o título "Bloodborne™").
3. Em **Game name**, coloque "Bloodborne". Para a capa, use **Search IGDB**, **Paste URL** ou **Local file**.
4. Clique em **Save**. Em até 5 s o Discord mostra o jogo. Use **Test** para mostrar o jogo sem ele estar aberto.

Para emuladores que rodam vários jogos com o mesmo exe, cadastre cada jogo com o mesmo exe e um filtro em **Only when → window title contains / command line contains**.

## O que o Discord mostra de verdade
- **Perfil / card de atividade**: o nome do jogo (campo `details`), a capa, a linha de status e o tempo jogado.
- **Lista de membros**: com **Show the game's name in the member list** ligado, o app envia `status_display_type = 2` (Details), e a lista mostra "Jogando Bloodborne" em vez do nome do Application. O campo está na documentação atual do Activity do Discord.
- **Título do card** ("Jogando <nome>" no topo do card): é sempre o nome do Application. Pelo IPC não dá para trocar esse nome por jogo. Por isso vale dar um nome genérico ao Application.
- A capa precisa ser uma URL `https://` pública com até 256 caracteres.

## Arquivos
- Configuração e lista de jogos: `%APPDATA%\Game Presence\config.json`
- Log: `%APPDATA%\Game Presence\app.log`

## Testes
```
go test ./...
```
Os testes da interface rodam sem abrir janela e usam uma configuração temporária, nunca a sua.

## Compilar
Precisa do Go 1.27 ou mais novo. Mantenha o projeto em um caminho só com caracteres ASCII.
```
cd C:\Projetos\gamepresence
go tool mygo build
```
