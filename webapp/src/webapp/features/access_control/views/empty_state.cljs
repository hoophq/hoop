(ns webapp.features.access-control.views.empty-state
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Button]]
   [re-frame.core :as rf]
   [webapp.components.button :as button]
   [webapp.components.headings :as h]))

(defn main [installed?]
  [:> Box {:class "flex flex-col items-center justify-center py-16 px-4 bg-white rounded-lg shadow-sm max-w-3xl mx-auto"}

   ;; Illustration from design
   [:> Box {:class "mb-8"}
    [:img {:src "/images/illustrations/empty-state.png"
           :alt "Empty state illustration"
           :class "w-64"}]]

   ;; Title and description
   [:> Flex {:direction "column" :align "center" :gap "3" :class "mb-8 text-center"}
    [:> Text {:size "5" :weight "bold" :class "text-gray-12"}
     "No Access Controls configured"]
    [:> Text {:size "3" :class "text-gray-11 max-w-md text-center"}
     "Activate to enable an additional security layer. When activated, users are not allowed to access connections by default unless permission is given for each one."]]

   ;; Action button
   (if installed?
     [:> Button {:size "3"
                 :class "bg-blue-600 hover:bg-blue-700"
                 :onClick #(rf/dispatch [:navigate :access-control-new])}
      "Configure Access Control"]

     ;; If not installed, show activate button
     [button/primary
      {:text "Activate Access Control"
       :variant :medium
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
                                    1000))}])}])

   ;; Documentation link
   [:> Flex {:align "center" :class "mt-8 text-sm"}
    [:> Text {:class "text-gray-11 mr-1"}
     "Need more information? Check out"]
    [:a {:href "#" :class "text-blue-600 hover:underline"}
     "access control documentation"]
    [:> Text {:class "text-gray-11 ml-1"}
     "."]]])
