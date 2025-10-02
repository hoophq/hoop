(ns webapp.features.runbooks.setup.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(defn main [installed?]
  [:> Box {:class "flex flex-col h-full items-center justify-between py-16 px-4 bg-white max-w-3xl mx-auto"}

   [:> Flex {:direction "column" :align "center"}
    [:> Box {:class "mb-8"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Empty state illustration"
            :class "w-96"}]]

    [:> Flex {:direction "column" :align "center" :gap "3" :class "mb-8 text-center"}
     [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
      (if installed?
        "No connections available to configure with Runbooks yet"
        "Activate to enable an additional automation layer. When activated, you can define which runbook paths are accessible for each connection.")]]

    (if installed?
      [:> Button {:size "3"
                  :onClick #(rf/dispatch [:navigate :connections])}
       "Create Connection"]

      [:> Button
       {:size "3"
        :on-click #(rf/dispatch
                    [:dialog->open
                     {:title "Activate Runbooks"
                      :text "By activating this feature you'll be able to define which runbook paths are accessible for each connection."
                      :text-action-button "Confirm"
                      :action-button? true
                      :type :info
                      :on-success (fn []
                                    (rf/dispatch [:plugins->create-plugin {:name "runbooks"
                                                                           :connections []}])
                                    (js/setTimeout
                                     (fn [] (rf/dispatch [:plugins->get-plugin-by-name "runbooks"]))
                                     1000))}])}
       "Activate Runbooks"])]

   [:> Flex {:align "center" :class "text-sm"}
    [:> Text {:class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:a {:href (config/docs-url :features :runbooks)
         :class "text-blue-600 hover:underline"}
     "runbooks documentation"]
    [:> Text {:class "text-gray-11 ml-1"}
     "."]]])
