(ns webapp.audit.views.session-data-video
  (:require
   [reagent.core :as r]
   [re-frame.core :as rf]
   ["asciinema-player" :as asciinema]
   ["fancy-ansi/react" :refer [AnsiHtml]]
   [webapp.audit.views.empty-event-stream :as empty-event-stream]
   [webapp.components.tabs :as tabs]
   [webapp.components.loaders :as loaders]
   [webapp.audit.views.terminal-decoder :as terminal-decoder]
   [webapp.utilities :as utilities]))

(defn- asciinema-player-container [{:keys [width height]} event-stream]
  (let [asciinema-view (atom {:current nil})
        event-stream-config [{"version" 2
                              "title" "Recording"
                              "width" width
                              "height" height
                              "env" {"TERM" "xterm-256color"}}]
        asciinema-options (clj->js {:loop true
                                    :fit "both"})
        asciinema-initiator #(.create asciinema
                                      (clj->js {"data" (concat
                                                        event-stream-config
                                                        event-stream)})
                                      %
                                      asciinema-options)
        component-did-mount #(swap! asciinema-view
                                    assoc
                                    :current
                                    (asciinema-initiator (:current @asciinema-view)))]
    (r/create-class {:display-name "asciinema-player"
                     :component-did-mount component-did-mount
                     :reagent-render
                     (fn []
                       [:div {:class (str "asciinema-player-mount "
                                          "flex h-full w-full min-h-0 flex-1 flex-col overflow-hidden")
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

(defn- logs-content [logs-text]
  (if (empty? logs-text)
    [empty-event-stream/main]
    [logs-text-container logs-text]))

(defn- tabbed-view [{:keys [selected-tab on-change logs size video-events]}]
  [:div {:class "flex flex-col h-[660px] min-h-0 overflow-hidden"}
   [tabs/tabs {:on-change on-change
               :tabs ["Logs" "Video"]
               :default-value "Logs"}]
   [:div {:class "flex min-h-0 flex-1 flex-col overflow-hidden"}
    (case selected-tab
      "Logs" logs
      "Video" [asciinema-player-container size video-events])]])

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
      [tabbed-view
       {:selected-tab @selected-tab
        :on-change handle-tab-change
        :logs (cond
                (= (:status @session-logs) :loading)
                [loading-logs]

                (and (= (:status @session-logs) :success)
                     (seq (:data @session-logs)))
                [logs-content (utilities/decode-b64 (first (:data @session-logs)))]

                :else
                [empty-event-stream/main])
        :size {:width 80 :height 24}
        :video-events (when (= @selected-tab "Video")
                        (terminal-decoder/decode-events event-stream))}])))

(defn main [event-stream session-id]
  [:div
   (if (empty? event-stream)
     [empty-event-stream/main]
     [tab-container event-stream session-id])])

(defn- ssh-tab-container [_recording]
  (let [selected-tab (r/atom "Logs")]
    (fn [{:keys [width height events]}]
      ;; The gateway's Logs text joins the raw frames, so build it from the
      ;; decoded terminal channels instead.
      (let [decoded (terminal-decoder/decode-byte-events events)]
        [tabbed-view
         {:selected-tab @selected-tab
          :on-change #(reset! selected-tab %)
          :logs [logs-content (->> decoded
                                   (filter #(contains? #{"o" "e"} (second %)))
                                   (map #(nth % 2))
                                   (apply str))]
          :size {:width width :height height}
          :video-events decoded}]))))

(defn ssh-terminal
  "Logs and Video for the pty channels of an SSH recording, as read by
  ssh-decoder/terminal-recording."
  [recording]
  [:div [ssh-tab-container recording]])
