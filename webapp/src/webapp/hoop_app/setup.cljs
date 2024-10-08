(ns webapp.hoop-app.setup
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
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
  (let [gateway-info (rf/subscribe [:gateway->info])
        grpc-url-parsed (.parse js/URL (-> @gateway-info :data :grpc_url))

        steps (r/atom {:download {:status "current"}
                       :connect {:status "upcoming"}})]
    (rf/dispatch [:hoop-app->get-my-configs])
    (pooling-get-app-status set-timeout-def)
    (rf/dispatch [:hoop-app->update-my-configs {:apiUrl (-> @gateway-info :data :api_url)
                                                :grpcUrl (.-host grpc-url-parsed)}])
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
                                                                            (change-step :connect "current")
                                                                            (js/window.open "https://install.hoop.dev"))}])
                                    (when-not  (= (:status (@steps :download)) "complete")
                                      [button/tailwind-secondary {:text "Already downloaded"
                                                                  :outlined? true
                                                                  :on-click (fn []
                                                                              (change-step :download "complete")
                                                                              (change-step :connect "current"))}])]}
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
                                                                                      (.setItem js/localStorage "hoop-connect-setup" "skipped")
                                                                                      (rf/dispatch [:connections->start-connect connection-name]))}]]}}}]]]]))))
