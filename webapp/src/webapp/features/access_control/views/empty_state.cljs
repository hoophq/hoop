(ns webapp.features.access-control.views.empty-state
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
        "No Access Control Groups available to manage yet"
        "Activate to enable an additional security layer. When activated, users are not allowed to access connections by default unless permission is given for each one.")]]

    (if installed?
      [:> Button {:size "3"
                  :onClick #(rf/dispatch [:navigate :access-control-new])}
       "Create Group"]

      [:> Button
       {:size "3"
        :on-click #(rf/dispatch
                    [:dialog->open
                     {:title "Activate Access Control"
                      :text "By activating this feature users will have their accesses blocked until a connection permission is set."
                      :text-action-button "Confirm"
                      :action-button? true
                      :type :info
                      :on-success (fn []
                                    (rf/dispatch [:plugins->create-plugin {:name "access_control"
                                                                           :connections []}])
                                    (js/setTimeout
                                     (fn [] (rf/dispatch [:plugins->get-plugin-by-name "access_control"]))
                                     1000))}])}
       "Activate Access Control"])]

   [:> Flex {:align "center" :class "text-sm"}
    [:> Text {:class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:a {:href (config/docs-url :features :access-control)
         :class "text-blue-600 hover:underline"}
     "access control documentation"]
    [:> Text {:class "text-gray-11 ml-1"}
     "."]]])
