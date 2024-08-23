(ns webapp.hoop-app.setup
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.stepper :as stepper]))

(def pooling-get-app-status
  (fn [set-timeout]
    (rf/dispatch [:hoop-app->get-app-status])
    (rf/dispatch [:hoop-app->get-my-configs])
    (set-timeout)))

(defn set-timeout-def []
  (js/setTimeout #(pooling-get-app-status set-timeout-def) 5000))

(defn main [_]
  (let [hoop-configs (rf/subscribe [:hoop-app->my-configs])
        app-running? (rf/subscribe [:hoop-app->running?])
        api-url (r/atom (or (:apiUrl @hoop-configs) ""))
        grpc-url (r/atom (or (:grpcUrl @hoop-configs) ""))
        steps (r/atom {:download {:status "current"}
                       :setup {:status "upcoming"}
                       :connect {:status "upcoming"}})]
    (rf/dispatch [:hoop-app->get-my-configs])
    (pooling-get-app-status set-timeout-def)
    (fn [connection-name]
      (let [change-step (fn [step status]
                          (reset! steps (update @steps step #(assoc % :status status))))]
        [:section
         [:div {:class "px-4"}
          [:header {:class "mb-2"}
           [h/h3 "Setup your hoop access"]]
          [:main
           [:p {:class "max-w-md text-sm text-gray-800 mb-2"}
            (str "Superpower your web experience with a native access to your "
                 "databases and services without the need of command line.")]
           [:div {:class "mb-6"}
            [button/tailwind-tertiary {:text "Skip setup and connect"
                                       :on-click (fn []
                                                   (js/clearTimeout)
                                                   (.setItem js/localStorage "hoop-connect-setup" "skipped")
                                                   (rf/dispatch [:connections->start-connect connection-name]))}]]
           [stepper/main
            {:download {:status (:status (@steps :download))
                        :title "Download hoop.dev app"
                        :text "Available for Windows, Linux and MacOS."
                        :component [:div {:class "flex gap-4"}
                                    (if (= (:status (@steps :download)) "complete")
                                      [button/tailwind-secondary {:text [:div {:class "flex gap-2"}
                                                                         [:> hero-outline-icon/ArrowDownTrayIcon {:class "h-5 w-5"}]
                                                                         "Download"]
                                                                  :outlined? true
                                                                  :on-click #(js/window.open "https://install.hoop.dev")}]

                                      [button/tailwind-primary {:text [:div {:class "flex gap-2"}
                                                                       [:> hero-outline-icon/ArrowDownTrayIcon {:class "h-5 w-5"}]
                                                                       "Download"]
                                                                :on-click (fn []
                                                                            (change-step :download "complete")
                                                                            (change-step :setup "current")
                                                                            (js/window.open "https://install.hoop.dev"))}])
                                    (when-not  (= (:status (@steps :download)) "complete")
                                      [button/tailwind-secondary {:text "Already downloaded"
                                                                  :outlined? true
                                                                  :on-click (fn []
                                                                              (change-step :download "complete")
                                                                              (change-step :setup "current"))}])]}
             :setup {:status (:status (@steps :setup))
                     :title "Open and install"
                     :text "Run downloaded file and follow installation instructions."
                     :extra-step {:title "Setup API and agent information"
                                  :text "Provide your URLs for hoop access."
                                  :component [:section
                                              [:div {:class "grid grid-cols-2 gap-4"}
                                               [forms/input {:label "API URL"
                                                             :on-change #(reset! api-url (-> % .-target .-value))
                                                             :placeholder "https://use.hoop.dev"
                                                             :disabled (not @app-running?)
                                                             :full-width true
                                                             :value @api-url}]
                                               [forms/input {:label "Agent URL"
                                                             :on-change #(reset! grpc-url (-> % .-target .-value))
                                                             :placeholder "use.hoop.dev:8443"
                                                             :disabled (not @app-running?)
                                                             :full-width true
                                                             :value @grpc-url}]]
                                              [:div {:class "flex items-center gap-3"}
                                               (if (= (:status (@steps :setup)) "complete")
                                                 [button/tailwind-secondary {:text "Save and continue"
                                                                             :disabled (not @app-running?)
                                                                             :type "button"
                                                                             :outlined? true
                                                                             :on-click (fn []
                                                                                         (rf/dispatch [:hoop-app->update-my-configs {:apiUrl @api-url
                                                                                                                                     :grpcUrl @grpc-url}])
                                                                                         (change-step :setup "complete")
                                                                                         (change-step :connect "current"))}]

                                                 [button/tailwind-primary {:text "Save and continue"
                                                                           :type "button"
                                                                           :disabled (not @app-running?)
                                                                           :on-click (fn []
                                                                                       (rf/dispatch [:hoop-app->update-my-configs {:apiUrl @api-url
                                                                                                                                   :grpcUrl @grpc-url}])
                                                                                       (change-step :setup "complete")
                                                                                       (change-step :connect "current"))}])
                                               (when (not @app-running?)
                                                 [:span {:class "text-gray-800 text-xs"}
                                                  "Your app is not running, please start it to continue."])]]}}
             :connect {:status (:status (@steps :connect))
                       :title "Login and Start"
                       :text "Access your app options to sign in to your hoop account and start a hoop access."
                       :component [:div
                                   [:figure {:class "flex"}
                                    [:img {:src "/images/hoop-app-tray.png"
                                           :alt "Hoop app tray screen"}]]]
                       :extra-step {:title (str "Connect to " connection-name)
                                    :text "Establish a hoop access to your connection once your hoop is indicated as online."
                                    :component [:div
                                                [button/tailwind-primary {:text [:div {:class "flex gap-2"}
                                                                                 [:> hero-outline-icon/SignalIcon {:class "h-5 w-5"}]
                                                                                 "Connect"]
                                                                          :on-click (fn []
                                                                                      (js/clearTimeout)
                                                                                      (rf/dispatch [:connections->start-connect connection-name]))}]]}}}]]]]))))
