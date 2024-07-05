(ns webapp.audit.views.session-data-video
  (:require
   [reagent.core :as r]
   ["asciinema-player" :as asciinema]
   [webapp.audit.views.empty-event-stream :as empty-event-stream]))

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

(defn main [event-stream]
  [:div
   (if (empty? event-stream)
     [empty-event-stream/main]
     [asciinema-player-container
      (map
       (fn [e]
         [(first e)
          (second e)
          (js/atob (nth e 2))]) event-stream)])])
