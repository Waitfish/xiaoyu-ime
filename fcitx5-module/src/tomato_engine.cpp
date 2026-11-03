// 番茄输入法 fcitx5 引擎模块（M1：模块内直接嵌入 librime）
//
// 架构（见 docs/architecture.md）：本模块是永久存在的"平台薄壳"，
// M2 会把引擎换成 Go 守护进程（daemon/），本文件只剩按键转发与 UI 同步。
//
// 按 fcitx5 5.1.7（Ubuntu 24.04/26.04）真实头文件编写：
//   引擎 = InputMethodEngineV2（本身就是 AddonInstance），工厂 create(AddonManager*)；
//   activate/deactivate/reset 收 InputContextEvent&，inputContext() 返回指针；
//   UI 走 InputContext::inputPanel()（setPreedit / setCandidateList(DisplayOnlyCandidateList)）
//   + updateUserInterface；提交用 commitString()；
//   IM 条目由 /usr/share/fcitx5/inputmethod/tomato.conf 声明。
#include <fcitx/addonfactory.h>
#include <fcitx/addoninstance.h>
#include <fcitx/candidatelist.h>
#include <fcitx/inputcontext.h>
#include <fcitx/inputmethodengine.h>
#include <fcitx/inputmethodentry.h>
#include <fcitx/inputpanel.h>
#include <fcitx/text.h>
#include <fcitx/userinterface.h>
#include <rime_api.h>

#include <cstdlib>
#include <map>
#include <memory>
#include <string>
#include <vector>

namespace {

class TomatoEngine : public fcitx::InputMethodEngineV2 {
public:
    TomatoEngine() { initRime(); }
    ~TomatoEngine() override {
        if (api_)
            api_->finalize();
    }

    void activate(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        if (!api_)
            return;
        if (RimeSessionId sid = api_->create_session())
            sessions_[event.inputContext()->uuid()] = sid;
    }

    void deactivate(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto it = sessions_.find(event.inputContext()->uuid());
        if (it == sessions_.end())
            return;
        if (api_)
            api_->destroy_session(it->second);
        sessions_.erase(it);
    }

    void reset(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto it = sessions_.find(event.inputContext()->uuid());
        if (it != sessions_.end() && api_)
            api_->clear_composition(it->second);
    }

    void keyEvent(const fcitx::InputMethodEntry &, fcitx::KeyEvent &keyEvent) override {
        auto *ic = keyEvent.inputContext();
        auto it = sessions_.find(ic->uuid());
        if (it == sessions_.end() || !api_ || keyEvent.isRelease())
            return;

        const auto key = keyEvent.key();
        if (isModifier(key.sym()))
            return;

        const bool handled =
            api_->process_key(it->second, static_cast<int>(key.sym()), rimeModifiers(key));
        pollCommit(*ic, it->second);
        updateUI(*ic, it->second);
        if (handled)
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

    void initRime() {
        api_ = rime_get_api();
        if (!api_)
            return;
        // 关键：api->setup() 不填 data_size；data_size 无效时 librime 忽略全部 traits 字段
        RimeTraits traits = {};
        traits.data_size = sizeof(traits);
        api_->setup(&traits);
        const char *home = getenv("HOME");
        const std::string userDir = std::string(home ? home : ".") + "/.local/share/tomato-ime/rime";
        traits.shared_data_dir = "/usr/share/rime-data";
        traits.user_data_dir = userDir.c_str();
        traits.app_name = "tomato-ime";
        traits.distribution_name = "Ubuntu";
        traits.distribution_code_name = "resolute";
        traits.distribution_version = "26.04";
        api_->initialize(&traits);
        api_->start_maintenance(true);
    }

    void pollCommit(fcitx::InputContext &ic, RimeSessionId sid) {
        RIME_STRUCT(RimeCommit, commit);
        if (api_->get_commit(sid, &commit)) {
            ic.commitString(commit.text);
            api_->free_commit(&commit);
        }
    }

    void updateUI(fcitx::InputContext &ic, RimeSessionId sid) {
        RIME_STRUCT(RimeContext, ctx);
        api_->get_context(sid, &ctx);

        auto &panel = ic.inputPanel();

        fcitx::Text preedit;
        if (ctx.composition.preedit && ctx.composition.length > 0)
            preedit.append(ctx.composition.preedit);
        panel.setPreedit(preedit);

        if (ctx.menu.candidates && ctx.menu.num_candidates > 0) {
            auto cl = std::make_unique<fcitx::DisplayOnlyCandidateList>();
            std::vector<fcitx::Text> contents;
            for (size_t i = 0; i < ctx.menu.num_candidates; ++i) {
                const auto &cand = ctx.menu.candidates[i];
                fcitx::Text text(cand.text);
                if (cand.comment && *cand.comment)
                    text.append(" " + std::string(cand.comment));
                contents.push_back(std::move(text));
            }
            cl->setContent(std::move(contents));
            cl->setCursorIndex(ctx.menu.highlighted_candidate_index);
            panel.setCandidateList(std::move(cl));
        } else {
            panel.setCandidateList(nullptr);
        }

        ic.updateUserInterface(fcitx::UserInterfaceComponent::InputPanel);
        api_->free_context(&ctx);
    }

    RimeApi *api_ = nullptr;
    std::map<fcitx::ICUUID, RimeSessionId> sessions_;
};

class TomatoModuleFactory : public fcitx::AddonFactory {
    fcitx::AddonInstance *create(fcitx::AddonManager *) override {
        return new TomatoEngine();
    }
};

} // namespace

FCITX_ADDON_FACTORY(TomatoModuleFactory)
