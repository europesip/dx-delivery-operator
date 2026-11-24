// SPDX-License-Identifier: MIT
/*
 * Copyright 2026 EUROPESIP. Licensed under the MIT License.
 *
 * EUROPESIP Showcase: a JSR 286 portlet to demonstrate DXJavaExtension (DX Delivery Operator, issue #54): a custom
 * Java portlet loaded from a registry onto the official DX Compose Web Engine image, with no custom image.
 * Plain Java (no JSP), portlet-api 2.0, Java 8.
 */
package com.europesip.showcase;

import java.io.IOException;
import java.io.InputStream;
import java.io.PrintWriter;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.Properties;
import java.util.TimeZone;

import javax.portlet.ActionRequest;
import javax.portlet.ActionResponse;
import javax.portlet.GenericPortlet;
import javax.portlet.PortletException;
import javax.portlet.PortletMode;
import javax.portlet.PortletPreferences;
import javax.portlet.PortletURL;
import javax.portlet.ProcessAction;
import javax.portlet.RenderRequest;
import javax.portlet.RenderResponse;

public class ShowcasePortlet extends GenericPortlet {

    private static final String[] TOPICS = {"DX Compose", "Kubernetes", "GitOps", "Harbor"};
    private String version = "dev";
    private String whatsNew = "";

    @Override
    public void init() throws PortletException {
        super.init();
        Properties p = new Properties();
        try (InputStream in = getClass().getClassLoader().getResourceAsStream("showcase.properties")) {
            if (in != null) {
                p.load(in);
            }
        } catch (IOException e) {
            // keep the defaults
        }
        version = p.getProperty("version", version);
        whatsNew = p.getProperty("whatsnew", "").trim();
        if (whatsNew.startsWith("${")) {
            whatsNew = "";
        }
    }

    private static String esc(String s) {
        if (s == null) {
            return "";
        }
        return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace("\"", "&quot;");
    }

    private static int intParam(RenderRequest r, String name) {
        try {
            return Integer.parseInt(r.getParameter(name));
        } catch (Exception e) {
            return 0;
        }
    }

    private static String accentOf(PortletPreferences prefs) {
        String a = prefs.getValue("accent", "#E6007E");
        return a.matches("#[0-9A-Fa-f]{6}") ? a : "#E6007E";
    }

    private void header(PrintWriter w, RenderResponse res, String accent) {
        String ns = res.getNamespace();
        w.println("<style>"
            + "." + ns + "card{font-family:Segoe UI,Arial,sans-serif;border-radius:14px;overflow:hidden;box-shadow:0 4px 18px rgba(0,0,0,.12);max-width:760px;margin:8px 0;background:#fff;color:#1d1d3a}"
            + "." + ns + "top{background:linear-gradient(110deg,#2D2E83 0%," + accent + " 100%);color:#fff;padding:18px 22px}"
            + "." + ns + "top h2{margin:0;font-size:22px;letter-spacing:.3px}"
            + "." + ns + "top p{margin:4px 0 0;opacity:.9;font-size:13px}"
            + "." + ns + "badge{display:inline-block;background:rgba(255,255,255,.22);border-radius:20px;padding:2px 10px;font-size:12px;margin-left:8px}"
            + "." + ns + "new{background:#FFD400;color:#1d1d3a;font-weight:700}"
            + "." + ns + "body{padding:16px 22px}"
            + "." + ns + "grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;margin:6px 0 14px}"
            + "." + ns + "tile{background:#F4F5FB;border-radius:10px;padding:8px 10px}"
            + "." + ns + "tile b{display:block;font-size:11px;text-transform:uppercase;color:#6b6c8a;letter-spacing:.5px}"
            + "." + ns + "tile span{font-size:14px}"
            + "." + ns + "hello{font-size:20px;font-weight:600;color:" + accent + ";margin:6px 0 12px}"
            + "." + ns + "row{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin:8px 0}"
            + "." + ns + "btn{background:" + accent + ";color:#fff;border:0;border-radius:8px;padding:7px 14px;cursor:pointer;text-decoration:none;font-size:13px}"
            + "." + ns + "chip{border:1px solid " + accent + ";color:" + accent + ";border-radius:16px;padding:3px 11px;text-decoration:none;font-size:12px}"
            + "." + ns + "chip.on{background:" + accent + ";color:#fff}"
            + "." + ns + "foot{border-top:1px solid #eee;padding:9px 22px;font-size:11px;color:#6b6c8a}"
            + "." + ns + "card input[type=text]{border:1px solid #ccd;border-radius:8px;padding:6px 9px;font-size:13px}"
            + "</style>");
    }

    @Override
    protected void doView(RenderRequest req, RenderResponse res) throws PortletException, IOException {
        res.setContentType("text/html");
        PrintWriter w = res.getWriter();
        PortletPreferences prefs = req.getPreferences();
        String accent = accentOf(prefs);
        String ns = res.getNamespace();
        String name = req.getParameter("name");
        String topic = req.getParameter("topic");
        int clicks = intParam(req, "clicks");
        Locale locale = req.getLocale();
        String user = req.getRemoteUser() == null ? "anonymous" : req.getRemoteUser();
        SimpleDateFormat f = new SimpleDateFormat("yyyy-MM-dd HH:mm:ss zzz", locale);
        f.setTimeZone(TimeZone.getTimeZone("Europe/Madrid"));

        header(w, res, accent);
        w.println("<div class=\"" + ns + "card\">");
        w.println("<div class=\"" + ns + "top\"><h2>EUROPESIP Showcase<span class=\"" + ns + "badge\">v" + esc(version) + "</span>"
            + (whatsNew.isEmpty() ? "" : "<span class=\"" + ns + "badge " + ns + "new\">NEW: " + esc(whatsNew) + "</span>") + "</h2>"
            + "<p>A JSR 286 portlet loaded from Harbor onto the official DX Compose Web Engine image &mdash; no custom image (DXJavaExtension)</p></div>");
        w.println("<div class=\"" + ns + "body\">");
        w.println("<div class=\"" + ns + "grid\">"
            + tile(ns, "Version", "v" + version)
            + tile(ns, "Server time", f.format(new Date()))
            + tile(ns, "User", user)
            + tile(ns, "Locale", locale.toString())
            + tile(ns, "Mode / state", req.getPortletMode() + " / " + req.getWindowState())
            + tile(ns, "Server", getPortletContext().getServerInfo())
            + "</div>");

        String greeting = prefs.getValue("greeting", "Hello");
        w.println("<div class=\"" + ns + "hello\">" + esc(greeting) + ", " + esc(name == null || name.trim().isEmpty() ? "DX Compose" : name.trim()) + "!</div>");

        PortletURL greet = res.createActionURL();
        greet.setParameter(ActionRequest.ACTION_NAME, "greet");
        greet.setParameter("clicks", String.valueOf(clicks));
        w.println("<form method=\"post\" action=\"" + greet + "\" class=\"" + ns + "row\">"
            + "<input type=\"text\" name=\"" + ns + "name\" placeholder=\"Your name\" maxlength=\"40\"/>"
            + "<button class=\"" + ns + "btn\" type=\"submit\">Greet me</button></form>");

        PortletURL click = res.createActionURL();
        click.setParameter(ActionRequest.ACTION_NAME, "click");
        click.setParameter("clicks", String.valueOf(clicks));
        if (name != null) {
            click.setParameter("name", name);
        }
        w.println("<div class=\"" + ns + "row\"><a class=\"" + ns + "btn\" href=\"" + click + "\">Click me</a>"
            + "<span>Clicks in this view: <b>" + clicks + "</b> (an action and a render parameter: JSR 286 round trip)</span></div>");

        StringBuilder chips = new StringBuilder("<div class=\"" + ns + "row\"><span>Topic (public render parameter):</span>");
        for (String t : TOPICS) {
            PortletURL u = res.createRenderURL();
            u.setParameter("topic", t);
            chips.append("<a class=\"").append(ns).append("chip").append(t.equals(topic) ? " on" : "")
                .append("\" href=\"").append(u).append("\">").append(esc(t)).append("</a>");
        }
        chips.append("</div>");
        w.println(chips);
        if (topic != null) {
            w.println("<p>Selected topic: <b>" + esc(topic) + "</b> &mdash; other portlets on the page that support <code>topic</code> receive it too.</p>");
        }
        if (req.isPortletModeAllowed(PortletMode.EDIT) && req.getRemoteUser() != null) {
            PortletURL edit = res.createRenderURL();
            edit.setPortletMode(PortletMode.EDIT);
            w.println("<p><a class=\"" + ns + "chip\" href=\"" + edit + "\">Edit greeting and colour (preferences)</a></p>");
        }
        w.println("</div>");
        w.println("<div class=\"" + ns + "foot\">EUROPESIP &middot; DX Delivery Operator &middot; DXJavaExtension &middot; MIT licence</div>");
        w.println("</div>");
    }

    private static String tile(String ns, String k, String v) {
        return "<div class=\"" + ns + "tile\"><b>" + esc(k) + "</b><span>" + esc(v) + "</span></div>";
    }

    @Override
    protected void doEdit(RenderRequest req, RenderResponse res) throws PortletException, IOException {
        res.setContentType("text/html");
        PrintWriter w = res.getWriter();
        PortletPreferences prefs = req.getPreferences();
        String accent = accentOf(prefs);
        String ns = res.getNamespace();
        header(w, res, accent);
        PortletURL save = res.createActionURL();
        save.setParameter(ActionRequest.ACTION_NAME, "save");
        w.println("<div class=\"" + ns + "card\"><div class=\"" + ns + "top\"><h2>EUROPESIP Showcase &middot; Edit</h2><p>Portlet preferences, stored by DX for this portlet</p></div>"
            + "<div class=\"" + ns + "body\"><form method=\"post\" action=\"" + save + "\">"
            + "<div class=\"" + ns + "row\">Greeting <input type=\"text\" name=\"" + ns + "greeting\" value=\"" + esc(prefs.getValue("greeting", "Hello")) + "\" maxlength=\"30\"/></div>"
            + "<div class=\"" + ns + "row\">Accent colour <input type=\"text\" name=\"" + ns + "accent\" value=\"" + esc(accent) + "\" maxlength=\"7\"/></div>"
            + "<div class=\"" + ns + "row\"><button class=\"" + ns + "btn\" type=\"submit\">Save</button></div></form></div></div>");
    }

    @ProcessAction(name = "greet")
    public void greet(ActionRequest req, ActionResponse res) {
        String name = req.getParameter(paramName(req, "name"));
        if (name == null) {
            name = req.getParameter("name");
        }
        if (name != null && !name.trim().isEmpty()) {
            res.setRenderParameter("name", name.trim().length() > 40 ? name.trim().substring(0, 40) : name.trim());
        }
        res.setRenderParameter("clicks", String.valueOf(parse(req.getParameter("clicks"))));
    }

    @ProcessAction(name = "click")
    public void click(ActionRequest req, ActionResponse res) {
        res.setRenderParameter("clicks", String.valueOf(parse(req.getParameter("clicks")) + 1));
        if (req.getParameter("name") != null) {
            res.setRenderParameter("name", req.getParameter("name"));
        }
    }

    @ProcessAction(name = "save")
    public void save(ActionRequest req, ActionResponse res) throws PortletException, IOException {
        PortletPreferences prefs = req.getPreferences();
        String g = req.getParameter(paramName(req, "greeting"));
        String a = req.getParameter(paramName(req, "accent"));
        if (g != null && !g.trim().isEmpty()) {
            prefs.setValue("greeting", g.trim());
        }
        if (a != null && a.trim().matches("#[0-9A-Fa-f]{6}")) {
            prefs.setValue("accent", a.trim());
        }
        prefs.store();
        res.setPortletMode(PortletMode.VIEW);
    }

    // Form fields are namespaced; some containers strip the namespace, others do not: accept both.
    private static String paramName(ActionRequest req, String n) {
        for (String p : req.getParameterMap().keySet()) {
            if (p.endsWith(n) && !p.equals(n)) {
                return p;
            }
        }
        return n;
    }

    private static int parse(String s) {
        try {
            return Integer.parseInt(s);
        } catch (Exception e) {
            return 0;
        }
    }
}
