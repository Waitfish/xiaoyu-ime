// 番茄输入法 fcitx5 引擎模块（M1：模块内直接嵌入 librime）
//
// 架构（见 docs/architecture.md）：本模块是永久存在的"平台薄壳"，
// M2 会把引擎换成 Go 守护进程（daemon/），本文件只剩按键转发与 UI 同步。
//
// 对照 fcitx5 5.1.7（Ubuntu 24.04/26.04）真实 API：
//   activate/deactivate/reset 收 InputContextEvent&；
//   会话按 InputContext::uuid() 保存；KeyEvent 用 isRelease()/filterAndAccept()。
#include <fcitx/addonfactory.h>
#include <fcitx/inputcontext.h>
#include <fcitx/inputmethodengine.h>
#include <fcitx/inputmethodentry.h>
#include <fcitx/instance.h>
#include <fcitx/text.h>
#include <fcitx/userinterface.h>
#include <rime_api.h>

#include <cstdlib>
#include <map>
#include <memory>
#include <string>

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
            sessions_[event.inputContext().uuid()] = sid;
    }

    void deactivate(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto it = sessions_.find(event.inputContext().uuid());
        if (it == sessions_.end())
            return;
        if (api_)
            api_->destroy_session(it->second);
        sessions_.erase(it);
    }

    void reset(const fcitx::InputMethodEntry &, fcitx::InputContextEvent &event) override {
        auto it = sessions_.find(event.inputContext().uuid());
        if (it != sessions_.end() && api_)
            api_->clear_composition(it->second);
    }

    void keyEvent(const fcitx::InputMethodEntry &, fcitx::KeyEvent &keyEvent) override {
        auto &ic = keyEvent.inputContext();
        auto it = sessions_.find(ic.uuid());
        if (it == sessions_.end() || !api_ || keyEvent.isRelease())
            return;

        const auto key = keyEvent.key();
        if (isModifier(key.sym()))
            return;

        const bool handled =
            api_->process_key(it->second, static_cast<int>(key.sym()), rimeModifiers(key));
        pollCommit(ic, it->second);
        updateUI(ic, it->second);
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
        unsigned int m = 0;
        if (key.modifiers() & fcitx::ModifierType::Ctrl)
            m |= rimeControlMask;
        if (key.modifiers() & fcitx::ModifierType::Alt)
            m |= rimeMod1Mask;
        if (key.modifiers() & fcitx::ModifierType::Shift)
            m |= rimeShiftMask;
        return m;
    }

    void initRime() {
        api_ = rime_get_api();
        if (!api_)
            return;
        RimeTraits traits;
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
            ic.commit(commit.text);
            api_->free_commit(&commit);
        }
    }

    void updateUI(fcitx::InputContext &ic, RimeSessionId sid) {
        RIME_STRUCT(RimeContext, ctx);
        api_->get_context(sid, &ctx);

        auto &comp = *ic.inputMethod()->composition();
        comp.clear();
        if (ctx.composition.preedit && ctx.composition.length > 0)
            comp.append(fcitx::Text::fromLegacy(ctx.composition.preedit));

        auto &cl = ic.userInterface()->candidateList();
        cl->clear();
        if (ctx.menu.candidates && ctx.menu.num_candidates > 0) {
            const int page = ctx.menu.page_no;
            const int pageSize = ctx.menu.page_size > 0 ? ctx.menu.page_size : 5;
            for (size_t i = 0; i < ctx.menu.num_candidates; ++i) {
                const auto &cand = ctx.menu.candidates[i];
                auto text = fcitx::Text::fromLegacy(cand.text);
                if (cand.comment && *cand.comment)
                    text.append(fcitx::Text(" " + std::string(cand.comment)));
                cl->append(std::move(text));
            }
            cl->setPageSize(pageSize);
            // RimeMenu 只有 is_last_page：总页数按当前页推算
            const int totalPages = ctx.menu.is_last_page ? page + 1 : page + 2;
            cl->setTotalCount(pageSize * totalPages);
            cl->setCursorIndex(page * pageSize + ctx.menu.highlighted_candidate_index);
        } else {
            cl->setCursorIndex(-1);
        }
        api_->free_context(&ctx);
    }

    RimeApi *api_ = nullptr;
    std::map<fcitx::ICUUID, RimeSessionId> sessions_;
};

class TomatoModule : public fcitx::Addon {
public:
    TomatoModule(fcitx::Instance *instance) {
        fcitx::InputMethodEntry entry;
        entry.setName("tomato");
        entry.setLongname("番茄拼音");
        entry.setShortname("番茄");
        entry.setDescription("番茄输入法：librime 引擎，番茄拼音方案");
        entry.setLanguage("zh_CN");
        engine_ = std::make_unique<TomatoEngine>();
        instance->inputMethodEngine()->registerEngine(std::move(entry), engine_.get());
    }

private:
    std::unique_ptr<TomatoEngine> engine_;
};

class TomatoModuleFactory : public fcitx::AddonFactory {
    fcitx::Addon *create(fcitx::Instance *instance) override {
        return new TomatoModule(instance);
    }
};

} // namespace

FCITX_ADDON_FACTORY(TomatoModuleFactory)
