(ns webapp.connections.views.form.toggle-review
  (:require [clojure.string :as cs]
            [webapp.components.headings :as h]
            [webapp.components.multiselect :as multi-select]
            [webapp.components.toggle :as toggle]))

(defn array->select-options [array]
  (mapv #(into {} {"value" % "label" (cs/lower-case (cs/replace % #"_" " "))}) array))

(defn main [{:keys [free-license?
                    user-groups
                    review-toggle-enabled?
                    approval-groups-value]}]
  [:div {:class "mb-large"}
   [:div {:class "mb-regular flex justify-between items-center"}
    [:div {:class "mr-24"}
     [:div {:class "flex items-center gap-2"}
      [h/h4-md "Enable Review"
       {:class (when free-license? "text-opacity-70")}]
      (when free-license?
        [:div {:class "text-blue-600 bg-blue-600 bg-opacity-10 rounded-md px-2 py-1 cursor-pointer"
               :on-click #(js/window.Intercom
                           "showNewMessage"
                           "I want to upgrade my current plan")}
         "Upgrade to Pro"])]
     [:label {:class "text-xs text-gray-500"}
      "Make sure everything gets reviewed before running"]]
    [toggle/main {:enabled? @review-toggle-enabled?
                  :disabled? free-license?
                  :on-click (fn []
                              (reset! review-toggle-enabled?
                                      (not @review-toggle-enabled?)))}]]
   (when @review-toggle-enabled?
     [multi-select/main {:options (array->select-options @user-groups)
                         :id "approval-groups-input"
                         :name "approval-groups-input"
                         :disabled? (or (not @review-toggle-enabled?) free-license?)
                         :required? @review-toggle-enabled?
                         :default-value (if @review-toggle-enabled?
                                          @approval-groups-value
                                          nil)
                         :on-change #(reset! approval-groups-value (js->clj %))}])])
