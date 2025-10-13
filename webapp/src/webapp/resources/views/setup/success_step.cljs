(ns webapp.resources.views.setup.success-step
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text Flex Checkbox]]
   ["lucide-react" :refer [ShieldCheck]]
   [re-frame.core :as rf]))

(defn main []
  (let [resource @(rf/subscribe [:resources->last-created])]
    [:> Box {:class "max-w-[600px] mx-auto p-8 text-center"}
     ;; Success icon
     [:> Flex {:justify "center" :class "mb-6"}
      [:> Box {:class "flex items-center justify-center w-16 h-16 rounded-full bg-blue-9"}
       [:> ShieldCheck {:size 32 :class "text-white"}]]]

     ;; Success message
     [:> Box {:class "space-y-4 mb-8"}
      [:> Heading {:as "h2" :size "6" :weight "bold" :class "text-[--gray-12]"}
       (:name resource "Your resource") " is ready"]
      [:> Text {:as "p" :size "3" :class "text-[--gray-11]"}
       "Every connection will be authenticated, audited, and secured."]]

     ;; Next steps checklist
     [:> Box {:class "bg-gray-2 rounded-lg p-6 text-left space-y-4"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
       "Next steps"]

      [:> Flex {:as "label" :gap "3" :align "center" :class "cursor-pointer hover:bg-gray-3 p-3 rounded"}
       [:> Checkbox {:size "2"}]
       [:> Box
        [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
         "Test Connection on Web Terminal"]
        [:> Text {:size "1" :class "text-[--gray-11] block"}
         "Recommended - Start using your resource immediately in your browser"]]]

      [:> Flex {:as "label" :gap "3" :align "center" :class "cursor-pointer hover:bg-gray-3 p-3 rounded"}
       [:> Checkbox {:size "2"}]
       [:> Box
        [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
         "Setup Native Access"]
        [:> Text {:size "1" :class "text-[--gray-11] block"}
         "Connect your IDE or database tools"]]]

      [:> Flex {:as "label" :gap "3" :align "center" :class "cursor-pointer hover:bg-gray-3 p-3 rounded"}
       [:> Checkbox {:size "2"}]
       [:> Box
        [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
         "Configure Additional Features"]
        [:> Text {:size "1" :class "text-[--gray-11] block"}
         "Advanced protection like AI Data Masking, Runbooks and more"]]]]

     ;; Skip button
     [:> Box {:class "mt-8"}
      [:button {:class "text-sm text-gray-11 hover:text-gray-12"
                :on-click #(rf/dispatch [:navigate :connections])}
       "Skip and configure later"]]]))
