(ns webapp.audit.views.session-data-video
  (:require
   [reagent.core :as r]
   [re-frame.core :as rf]
   ["asciinema-player" :as asciinema]
   ["fancy-ansi/react" :refer [AnsiHtml]]
   [webapp.audit.views.empty-event-stream :as empty-event-stream]
   [webapp.components.tabs :as tabs]
   [webapp.components.loaders :as loaders]
   [webapp.utilities :as utilities]))

(defn- asciinema-player-container [event-stream]
  (let [asciinema-view (atom {:current nil})
        event-stream-config [{"version" 2
                              "title" "Recording"
                              "width" 80
                              "height" 24
                              "env" {"TERM" "xterm-256color"}}]
        asciinema-options {:loop true
                           :theme "solarized-dark"}
        asciinema-initiator #(.create asciinema
                                      (clj->js {"data" (concat
                                                        event-stream-config
                                                        event-stream)})
                                      % asciinema-options)
        component-did-mount #(swap! asciinema-view
                                    assoc
                                    :current
                                    (asciinema-initiator (:current @asciinema-view)))]
    (r/create-class {:display-name "asciinema-player"
                     :component-did-mount component-did-mount
                     :reagent-render
                     (fn []
                       [:div {:id "asciinema-player-container"
                              :class ""
                              :ref #(swap! asciinema-view assoc :current %)}])})))

(defn- logs-text-container [logs-text]
  [:section
   {:class (str "relative bg-gray-900 font-mono overflow-auto h-[600px]"
                " whitespace-pre text-gray-200 text-sm"
                " p-radix-4 rounded-lg group")}
   [:div
    {:class "overflow-auto whitespace-pre h-full"}
    [:> AnsiHtml {:text logs-text
                  :className "font-mono whitespace-pre text-sm"}]]])

(defn- loading-logs []
  [:div {:class "flex gap-small items-center justify-center py-large"}
   [:span {:class "italic text-xs text-gray-600"}
    "Loading logs for this session"]
   [loaders/simple-loader {:size 4}]])

(defn- tab-container [_ session-id]
  (let [selected-tab (r/atom "Logs")
        session-logs (rf/subscribe [:audit->session-logs])
        handle-tab-change (fn [tab-name]
                            (reset! selected-tab tab-name)
                            (when (and (= tab-name "Logs")
                                       (not (:data @session-logs)))
                              (rf/dispatch [:audit->get-session-logs-data session-id])))]

    (rf/dispatch [:audit->get-session-logs-data session-id])

    (fn [event-stream]
      [:div {:class "flex flex-col h-[660px] overflow-y-hidden"}
       [tabs/tabs {:on-change handle-tab-change
                   :tabs ["Logs" "Video"]
                   :default-value "Logs"}]
       (case @selected-tab
         "Logs" (cond
                  (= (:status @session-logs) :loading)
                  [loading-logs]

                  (and (= (:status @session-logs) :success)
                       (seq (:data @session-logs)))
                  (let [event-data (first (:data @session-logs))
                        logs-text (utilities/decode-b64 event-data)]
                    (if (empty? logs-text)
                      [empty-event-stream/main]
                      [logs-text-container logs-text]))

                  (= (:status @session-logs) :error)
                  [empty-event-stream/main]

                  :else
                  [empty-event-stream/main])

         "Video" [asciinema-player-container
                  (map
                   (fn [e]
                     [(first e)
                      (second e)
                      (js/atob (nth e 2))]) event-stream)])])))

(defn main [event-stream session-id]
  [:div
   (if (empty? event-stream)
     [empty-event-stream/main]
     [tab-container event-stream session-id])])
