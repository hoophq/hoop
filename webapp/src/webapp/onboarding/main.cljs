(ns webapp.onboarding.main
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            ["@radix-ui/themes" :refer [Box Heading Spinner Text]]
            [webapp.components.loaders :as loaders]))

(defn check-for-regular-agents []
  (let [agents (rf/subscribe [:agents])
        check-interval (r/atom nil)
        transition-timeout (r/atom nil)]

    ;; Start polling for regular agents
    (rf/dispatch [:agents->get-agents])

    (reset! check-interval
            (js/setInterval
             #(rf/dispatch [:agents->get-agents])
             5000))

    (fn []
      (let [agents-data (get-in @agents [:data] [])
            agents-loaded? (= :ready (get @agents :status))
            agents-available? (and agents-loaded? (seq agents-data))]

        (if (and agents-available? @check-interval (not @transition-timeout))
          (do
            ;; Clear interval when agents are available
            (js/clearInterval @check-interval)
            (reset! check-interval nil)

            ;; Add 2 second delay before transitioning
            (reset! transition-timeout
                    (js/setTimeout
                     (fn []
                       ;; Now proceed to check embedded agents
                       (rf/dispatch [:agents->get-embedded-agents-connected])
                       (reset! transition-timeout nil))
                     2000))

            ;; Show loading screen during the transition delay
            [:> Box {:class "flex flex-col items-center justify-center h-screen"}
             [:> Box {:class "max-w-[600px] text-center space-y-6"}
              [:> Spinner {:size "3"}]
              [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12] mt-6"}
               "Preparing your environment"]
              [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
               "We're working on setting up everything before you get started."]
              [:> Text {:as "p" :size "2" :class "text-[--gray-11] mt-4"}
               "This might take a moment as we ensure all necessary agents are ready for your connection. While you wait, feel free to learn more about how Agents work in our documentation: "
               [:a {:href "https://hoop.dev/docs/concepts/agents"
                    :target "_blank"
                    :class "text-blue-500 hover:underline"}
                "https://hoop.dev/docs/concepts/agents"]]]])

          ;; Show loading screen while waiting for agents
          [:> Box {:class "flex flex-col items-center justify-center h-screen"}
           [:> Box {:class "max-w-[600px] text-center space-y-6"}
            [:> Spinner {:size "3"}]
            [:> Heading {:as "h3" :size "5" :weight "medium" :class "text-[--gray-12] mt-6"}
             "Preparing your environment"]
            [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
             "We're working on setting up everything before you get started."]
            [:> Text {:as "p" :size "2" :class "text-[--gray-11] mt-4"}
             "This might take a moment as we ensure all necessary agents are ready for your connection. While you wait, feel free to learn more about how Agents work in our documentation: "
             [:a {:href "https://hoop.dev/docs/concepts/agents"
                  :target "_blank"
                  :class "text-blue-500 hover:underline"}
              "https://hoop.dev/docs/concepts/agents"]]]])))))

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        connections (rf/subscribe [:connections])
        check-performed? (atom false)]

  ;; Dispatch data loading
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:connections->get-connections])

    (fn []
      (let [user-loading? (:loading @user)
            connections-loading? (:loading @connections)]

      ;; Only perform check when both data are loaded and check hasn't been performed yet
        (when (and (not user-loading?)
                   (not connections-loading?)
                   (not @check-performed?))
          (reset! check-performed? true)
        ;; Check for regular agents first before checking embedded agents
          (rf/dispatch [:agents->get-agents]))

      ;; Show loading while either data is still loading
        (if (or user-loading? connections-loading?)
          [:div.flex.items-center.justify-center.h-screen
           [loaders/simple-loader]]

        ;; Once data is loaded, check for regular agents first
          [check-for-regular-agents])))))
