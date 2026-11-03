// 番茄输入法 fcitx5 引擎模块（M2：引擎在 Go 守护进程，本模块只做按键转发与 UI 同步）
//
// 架构（docs/architecture.md）：模块加载进 fcitx5 进程，绝不嵌入 Go/librime 运行时；
// 引擎在 tomato-ime-daemon（unix socket，换行分隔 JSON）。连接失败时自动拉起守护进程。
//
// 按 fcitx5 5.1.7 真实头文件编写（要点见 AGENTS.md 铁律）。
#include <fcitx/addonfactory.h>
#include <fcitx/addoninstance.h>
#include <fcitx/candidatelist.h>
#include <fcitx/inputcontext.h>
#include <fcitx/inputmethodengine.h>
#include <fcitx/inputmethodentry.h>
#include <fcitx/inputpanel.h>
#include <fcitx/text.h>
#include <fcitx/userinterface.h>
#include <nlohmann/json.hpp>

#include <cerrno>
#include <cstdlib>
#include <map>
#include <poll.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <fcntl.h>
#include <memory>
#include <string>
#include <vector>

namespace {

using json = nlohmann::json;

class RimeClient {
public:
    ~RimeClient() { closeSocket(); }

    // 确保连接可用；必要时拉起守护进程并重试。
    bool ensureConnected() {
        if (fd_ >= 0)
            return true;
        for (int attempt = 0; attempt < 6; ++attempt) {
            fd_ = tryConnect();
            if (fd_ >= 0)
                return true;
            if (attempt == 0)
                spawnDaemon();
            usleep(120000);
        }
        return false;
    }

    // 发送一个请求并读取一行响应；失败返回 null（连接会被丢弃）。
    json call(const json &req) {
        if (fd_ < 0)
            return nullptr;
        const std::string line = req.dump() + "\n";
        if (!writeAll(line)) {
            closeSocket();
            return nullptr;
        }
        std::string respLine;
        if (!readLine(respLine)) {
            closeSocket();
            return nullptr;
        }
        json parsed = json::parse(respLine, nullptr, false);
        if (parsed.is_discarded()) {
            closeSocket();
            return nullptr;
        }
        return parsed;
    }

private:
    static std::string socketPath() {
        const char *xdg = getenv("XDG_RUNTIME_DIR");
        std::string dir = xdg && *xdg ? xdg : "/tmp";
        return dir + "/tomato-ime.sock";
    }

    static int tryConnect() {
        int fd = ::socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
        if (fd < 0)
            return -1;
        sockaddr_un addr{};
        addr.sun_family = AF_UNIX;
        strncpy(addr.sun_path, socketPath().c_str(), sizeof(addr.sun_path) - 1);
        if (::connect(fd, reinterpret_cast<sockaddr *>(&addr), sizeof(addr)) != 0) {
            ::close(fd);
            return -1;
        }
        return fd;
    }

    void spawnDaemon() {
        static const char *candidates[] = {
            "/usr/libexec/tomato-ime-daemon",
            "/usr/lib/tomato-ime/tomato-ime-daemon",
        };
        const char *path = nullptr;
        for (const char *c : candidates) {
            if (access(c, X_OK) == 0) {
                path = c;
                break;
            }
        }
        if (!path)
            return;
        pid_t pid = fork();
        if (pid == 0) {
            setsid();
            int devnull = open("/dev/null", O_RDWR);
            if (devnull >= 0) {
                dup2(devnull, 0);
                dup2(devnull, 1);
                dup2(devnull, 2);
            }
            execl(path, path, static_cast<char *>(nullptr));
            _exit(127);
        }
    }

    bool writeAll(const std::string &data) {
        size_t off = 0;
        while (off < data.size()) {
            ssize_t n = ::send(fd_, data.data() + off, data.size() - off, MSG_NOSIGNAL);
            if (n <= 0)
                return false;
            off += static_cast<size_t>(n);
        }
        return true;
    }

    bool readLine(std::string &out) {
        out.clear();
        char c;
        while (true) {
            struct pollfd pfd{fd_, POLLIN, 0};
            int pr = ::poll(&pfd, 1, 800);
            if (pr <= 0)
                return false;
            ssize_t n = ::recv(fd_, &c, 1, 0);
            if (n != 1)
                return false;
            if (c == '\n')
                return true;
            out.push_back(c);
            if (out.size() > 4u << 20)
                return false;
        }
    }

    void closeSocket() {
        if (fd_ >= 0) {
            ::close(fd_);
            fd_ = -1;
        }
    }

    int fd_ = -1;
};

class TomatoEngine : public fcitx::InputMethodEngineV2 {
public:
    void activate(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        if (!client_.ensureConnected())
            return;
        auto resp = client_.call({{"op", "open"}});
        if (resp.is_null() || !resp.contains("session"))
            return;
        sessions_[event.inputContext()->uuid()] = resp["session"].get<uint32_t>();
    }

    void deactivate(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto it = sessions_.find(event.inputContext()->uuid());
        if (it == sessions_.end())
            return;
        client_.call({{"op", "close"}, {"session", it->second}});
        sessions_.erase(it);
    }

    void reset(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto it = sessions_.find(event.inputContext()->uuid());
        if (it != sessions_.end())
            client_.call({{"op", "reset"}, {"session", it->second}});
    }

    void keyEvent(const fcitx::InputMethodEntry &, fcitx::KeyEvent &keyEvent) override {
        auto *ic = keyEvent.inputContext();
        auto it = sessions_.find(ic->uuid());
        if (it == sessions_.end() || keyEvent.isRelease())
            return;

        const auto key = keyEvent.key();
        if (isModifier(key.sym()))
            return;

        auto resp = client_.call({{"op", "key"},
                                  {"session", it->second},
                                  {"keysym", static_cast<int>(key.sym())},
                                  {"mods", rimeModifiers(key)}});
        if (resp.is_null())
            return; // 守护进程不可用：放行按键，应用收到原始输入

        if (resp.contains("commit"))
            ic->commitString(resp["commit"].get<std::string>());
        // M3：MyGo 候选窗在线时由它接管显示，内置面板清空
        const bool customUI = resp.value("ui_connected", false);
        updateUI(*ic, resp, customUI);
        sendCursor(*ic, it->second);
        if (resp.value("handled", false))
            keyEvent.filterAndAccept();
    }

private:
    static bool isModifier(fcitx::KeySym sym) {
        switch (sym) {
        case FcitxKey_Shift_L:
        case FcitxKey_Shift_R:
        case FcitxKey_Control_L:
        case FcitxKey_Control_R:
        case FcitxKey_Alt_L:
        case FcitxKey_Alt_R:
        case FcitxKey_Super_L:
        case FcitxKey_Super_R:
        case FcitxKey_Caps_Lock:
            return true;
        default:
            return false;
        }
    }

    static unsigned int rimeModifiers(const fcitx::Key &key) {
        // librime 用 X11 修饰掩码（新版头文件不再导出枚举，值不变）
        constexpr unsigned rimeShiftMask = 1u << 0;
        constexpr unsigned rimeControlMask = 1u << 2;
        constexpr unsigned rimeMod1Mask = 1u << 3;
        const auto states = key.states();
        unsigned int m = 0;
        if (states & fcitx::KeyState::Ctrl)
            m |= rimeControlMask;
        if (states & fcitx::KeyState::Alt)
            m |= rimeMod1Mask;
        if (states & fcitx::KeyState::Shift)
            m |= rimeShiftMask;
        return m;
    }

    void sendCursor(fcitx::InputContext &ic, uint32_t session) {
        const auto rect = ic.cursorRect();
        client_.call({{"op", "ui"},
                      {"session", session},
                      {"x", rect.x()},
                      {"y", rect.y()},
                      {"w", rect.width()},
                      {"h", rect.height()},
                      {"scale", ic.scaleFactor()}});
    }

    static void updateUI(fcitx::InputContext &ic, const json &resp, bool suppress) {
        auto &panel = ic.inputPanel();

        if (suppress) { // 候选窗接管：清空内置面板
            panel.setPreedit(fcitx::Text());
            panel.setCandidateList(nullptr);
            ic.updateUserInterface(fcitx::UserInterfaceComponent::InputPanel);
            return;
        }

        fcitx::Text preedit;
        if (resp.contains("preedit"))
            preedit.append(resp["preedit"].get<std::string>());
        panel.setPreedit(preedit);

        if (resp.contains("candidates") && !resp["candidates"].empty()) {
            auto cl = std::make_unique<fcitx::DisplayOnlyCandidateList>();
            std::vector<fcitx::Text> contents;
            for (const auto &cand : resp["candidates"]) {
                fcitx::Text text(cand.value("text", ""));
                const std::string comment = cand.value("comment", "");
                if (!comment.empty())
                    text.append(" " + comment);
                contents.push_back(std::move(text));
            }
            cl->setContent(std::move(contents));
            cl->setCursorIndex(resp.value("highlight", 0));
            panel.setCandidateList(std::move(cl));
        } else {
            panel.setCandidateList(nullptr);
        }

        ic.updateUserInterface(fcitx::UserInterfaceComponent::InputPanel);
    }

    RimeClient client_;
    std::map<fcitx::ICUUID, uint32_t> sessions_;
};

class TomatoModuleFactory : public fcitx::AddonFactory {
    fcitx::AddonInstance *create(fcitx::AddonManager *) override {
        return new TomatoEngine();
    }
};

} // namespace

FCITX_ADDON_FACTORY(TomatoModuleFactory)
