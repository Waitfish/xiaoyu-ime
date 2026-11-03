/* 小鱼输入法设置（GTK3 原生）。
 * 为什么不用 MyGo：候选窗在 wlroots 上继续用 MyGo；但设置窗要在 GNOME 上
 * 从菜单打开，MyGo（Go 界面框架，自带 CJK 排版）首帧会把 2 核虚拟机打满，
 * 整个桌面卡死数秒（实测进程瞬时 CPU 42%+、用户可复现冻结）。GTK3 与桌面
 * 共享 Pango/字体缓存，zenity 同栈实测秒开。
 * 与 daemon 走同一个 unix socket：get_config / set_config，即时生效+持久化。
 */
#include <gtk/gtk.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <errno.h>

typedef struct {
	char schema[64];
	gboolean ascii_mode, simplification, full_shape;
} Conf;

static GtkWidget *win, *status;
static GtkWidget *radio[3];
static GtkWidget *sw_ascii, *sw_simp, *sw_shape;
static const char *schemas[3] = {"luna_pinyin_simp", "double_pinyin_flypy", "wubi86"};

static char *socket_path(void) {
	const char *rd = g_getenv("XDG_RUNTIME_DIR");
	if (!rd || !*rd) rd = "/tmp";
	return g_strdup_printf("%s/xiaoyu-ime.sock", rd);
}

/* 极简 JSON 取值：格式由自家 daemon 控制，够用即可。 */
static const char *json_str(const char *j, const char *key, char *out, int n) {
	char pat[128];
	snprintf(pat, sizeof pat, "\"%s\":\"", key);
	const char *p = strstr(j, pat);
	if (!p) return NULL;
	p += strlen(pat);
	const char *q = strchr(p, '"');
	if (!q || q - p >= n) return NULL;
	memcpy(out, p, q - p);
	out[q - p] = 0;
	return out;
}
static gboolean json_bool(const char *j, const char *key, gboolean dflt) {
	char pat[128];
	snprintf(pat, sizeof pat, "\"%s\":", key);
	const char *p = strstr(j, pat);
	if (!p) return dflt;
	p += strlen(pat);
	return strncmp(p, "true", 4) == 0 ? TRUE : (strncmp(p, "false", 5) == 0 ? FALSE : dflt);
}

/* 收发一条请求；resp 需预清零。返回 0 成功。 */
static int daemon_call(const char *req, char *resp, size_t rsz) {
	char *path = socket_path();
	int fd = socket(AF_UNIX, SOCK_STREAM, 0);
	if (fd < 0) { g_free(path); return -1; }
	struct sockaddr_un au = {0};
	au.sun_family = AF_UNIX;
	snprintf(au.sun_path, sizeof au.sun_path, "%s", path);
	g_free(path);
	if (connect(fd, (struct sockaddr *)&au, sizeof au) < 0) { close(fd); return -1; }
	if (write(fd, req, strlen(req)) < 0) { close(fd); return -1; }
	size_t off = 0;
	for (;;) {
		ssize_t n = read(fd, resp + off, rsz - off - 1);
		if (n <= 0) break;
		off += n;
		resp[off] = 0;
		if (memchr(resp, '\n', off)) break;
		if (off + 1 >= rsz) break;
	}
	close(fd);
	return off > 0 ? 0 : -1;
}

static void load_conf(Conf *c) {
	strcpy(c->schema, schemas[0]);
	c->ascii_mode = FALSE; c->simplification = TRUE; c->full_shape = FALSE;
	char resp[4096] = {0};
	if (daemon_call("{\"op\":\"get_config\"}\n", resp, sizeof resp) != 0) return;
	char s[64];
	if (json_str(resp, "schema", s, sizeof s)) {
		for (int i = 0; i < 3; i++)
			if (strcmp(s, schemas[i]) == 0) { strcpy(c->schema, s); break; }
	}
	c->ascii_mode = json_bool(resp, "ascii_mode", FALSE);
	c->simplification = json_bool(resp, "simplification", TRUE);
	c->full_shape = json_bool(resp, "full_shape", FALSE);
}

static void on_apply(GtkButton *b, gpointer u) {
	(void)b; (void)u;
	int idx = 0;
	for (int i = 0; i < 3; i++)
		if (gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(radio[i]))) idx = i;
	char req[512];
	snprintf(req, sizeof req,
		"{\"op\":\"set_config\",\"config\":{\"schema\":\"%s\",\"ascii_mode\":%s,"
		"\"simplification\":%s,\"full_shape\":%s}}\n",
		schemas[idx],
		gtk_switch_get_active(GTK_SWITCH(sw_ascii)) ? "true" : "false",
		gtk_switch_get_active(GTK_SWITCH(sw_simp)) ? "true" : "false",
		gtk_switch_get_active(GTK_SWITCH(sw_shape)) ? "true" : "false");
	char resp[4096] = {0};
	if (daemon_call(req, resp, sizeof resp) == 0 && !strstr(resp, "\"error\""))
		gtk_label_set_text(GTK_LABEL(status), "已应用（对当前所有输入窗口即时生效）");
	else if (strstr(resp, "\"error\"")) {
		char e[256] = {0};
		json_str(resp, "error", e, sizeof e);
		char msg[320];
		snprintf(msg, sizeof msg, "应用失败：%s", e);
		gtk_label_set_text(GTK_LABEL(status), msg);
	} else
		gtk_label_set_text(GTK_LABEL(status), "应用失败：未连接守护进程");
}

static GtkWidget *labeled_switch(const char *label, const char *hint, GtkWidget **sw_out) {
	GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
	GtkWidget *sw = gtk_switch_new();
	gtk_widget_set_valign(sw, GTK_ALIGN_CENTER);
	GtkWidget *lb = gtk_label_new(label);
	GtkWidget *hb = gtk_label_new(hint);
	gtk_widget_set_halign(hb, GTK_ALIGN_START);
	gtk_label_set_xalign(GTK_LABEL(hb), 0.0);
	GtkStyleContext *sc = gtk_widget_get_style_context(hb);
	gtk_style_context_add_class(sc, "dim-label");
	gtk_box_pack_start(GTK_BOX(row), sw, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(row), lb, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(row), hb, TRUE, TRUE, 0);
	*sw_out = sw;
	return row;
}

int main(int argc, char **argv) {
	gtk_init(&argc, &argv);
	Conf c;
	load_conf(&c);

	win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	gtk_window_set_title(GTK_WINDOW(win), "小鱼输入法设置");
	gtk_window_set_default_size(GTK_WINDOW(win), 430, 460);
	gtk_window_set_resizable(GTK_WINDOW(win), FALSE);
	g_signal_connect(win, "destroy", G_CALLBACK(gtk_main_quit), NULL);

	GtkWidget *root = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
	gtk_container_set_border_width(GTK_CONTAINER(root), 16);
	gtk_container_add(GTK_CONTAINER(win), root);

	GtkWidget *title = gtk_label_new("小鱼输入法设置");
	{
		PangoAttrList *al = pango_attr_list_new();
		pango_attr_list_insert(al, pango_attr_scale_new(1.35));
		pango_attr_list_insert(al, pango_attr_weight_new(PANGO_WEIGHT_BOLD));
		gtk_label_set_attributes(GTK_LABEL(title), al);
		pango_attr_list_unref(al);
	}
	gtk_widget_set_halign(title, GTK_ALIGN_START);
	gtk_box_pack_start(GTK_BOX(root), title, FALSE, FALSE, 0);

	GtkWidget *sec1 = gtk_label_new("输入方案");
	gtk_widget_set_halign(sec1, GTK_ALIGN_START);
	gtk_box_pack_start(GTK_BOX(root), sec1, FALSE, FALSE, 0);

	GtkWidget *grp = NULL;
	const char *names[3] = {"全拼", "双拼（小鹤）", "五笔"};
	for (int i = 0; i < 3; i++) {
		radio[i] = gtk_radio_button_new_with_label_from_widget(GTK_RADIO_BUTTON(grp), names[i]);
		grp = radio[i];
		if (strcmp(c.schema, schemas[i]) == 0)
			gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(radio[i]), TRUE);
		gtk_box_pack_start(GTK_BOX(root), radio[i], FALSE, FALSE, 0);
	}
	if (!gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(radio[0])) &&
	    !gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(radio[1])) &&
	    !gtk_toggle_button_get_active(GTK_TOGGLE_BUTTON(radio[2])))
		gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(radio[0]), TRUE);

	gtk_box_pack_start(GTK_BOX(root), gtk_separator_new(GTK_ORIENTATION_HORIZONTAL), FALSE, FALSE, 4);

	GtkWidget *sec2 = gtk_label_new("默认输入状态（新输入窗口生效）");
	gtk_widget_set_halign(sec2, GTK_ALIGN_START);
	gtk_box_pack_start(GTK_BOX(root), sec2, FALSE, FALSE, 0);

	gtk_box_pack_start(GTK_BOX(root),
		labeled_switch("英文模式", "开 = 直接英文输入", &sw_ascii), FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(root),
		labeled_switch("简体输出", "关 = 繁体", &sw_simp), FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(root),
		labeled_switch("全角模式", "开 = 全角标点/字母", &sw_shape), FALSE, FALSE, 0);
	gtk_switch_set_active(GTK_SWITCH(sw_ascii), c.ascii_mode);
	gtk_switch_set_active(GTK_SWITCH(sw_simp), c.simplification);
	gtk_switch_set_active(GTK_SWITCH(sw_shape), c.full_shape);

	gtk_box_pack_start(GTK_BOX(root), gtk_separator_new(GTK_ORIENTATION_HORIZONTAL), FALSE, FALSE, 4);

	GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
	GtkWidget *btn = gtk_button_new_with_label("应用");
	g_signal_connect(btn, "clicked", G_CALLBACK(on_apply), NULL);
	status = gtk_label_new("");
	gtk_label_set_xalign(GTK_LABEL(status), 0.0);
	gtk_box_pack_start(GTK_BOX(row), btn, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(row), status, TRUE, TRUE, 0);
	gtk_box_pack_start(GTK_BOX(root), row, FALSE, FALSE, 0);

	gtk_widget_show_all(win);
	gtk_main();
	return 0;
}
