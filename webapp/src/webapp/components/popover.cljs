(ns webapp.components.popover)

(defn right
  "popover to be aligned on the right of the parent component.
  Parent component must have position: relative CSS property set."
  [{:keys [open component on-click-outside]}]
  (if (= open true)
    [:div {:class "absolute top-full w-full z-20"}
     [:div {:class "fixed inset-0 bg-gray-50 w-full h-full z-10 opacity-30"
            :on-click on-click-outside}]
     [:div {:class "absolute z-10 min-w-36 bg-white border shadow top-full mt-2 -right-3 rounded-lg"}
      [:div {:id "popover-container"
             :class "w-max"} component]
      [:div
       {:class (str "absolute w-3 h-3 "
                    "right-4 -top-1.5 "
                    "bg-white border-t border-l "
                    "rounded transform rotate-45")}]]]
    nil))

(defn top
  "popover to be aligned on the top of the parent component.
  Parent component must have position: relative CSS property set."
  [{:keys [open component on-click-outside]}]
  (if (= open true)
    [:div {:class "absolute bottom-full w-full z-20"}
     [:div {:class "fixed inset-0 bg-gray-50 w-full h-full z-10 opacity-30"
            :on-click on-click-outside}]
     [:div {:class "absolute z-10 min-w-36 bg-white border shadow bottom-0 mb-2 -right-3 rounded-lg"}
      [:div {:id "popover-container"
             :class "w-max"} component]
      [:div
       {:class (str "absolute w-3 h-3 "
                    "right-4 -bottom-1.5 "
                    "bg-white border-b border-r "
                    "rounded transform rotate-45")}]]]
    nil))

(defn bottom
  "popover to be aligned on the top of the parent component.
  Parent component must have position: relative CSS property set."
  [{:keys [open component on-click-outside]}]
  (if (= open true)
    [:div {:class "absolute bottom-full w-full z-20"}
     [:div {:class "fixed inset-0 bg-gray-50 w-full h-full z-10 opacity-30"
            :on-click on-click-outside}]
     [:div {:class "absolute z-10 min-w-36 bg-white border shadow top-0 mt-2 -right-3 rounded-lg"}
      [:div {:id "popover-container"
             :class "w-max"} component]
      [:div
       {:class (str "absolute w-3 h-3 "
                    "right-4 -top-1.5 "
                    "bg-white border-t border-l "
                    "rounded transform rotate-45")}]]]
    nil))

(defn left
  "popover to be aligned on the right of the parent component.
  Parent component must have position: relative CSS property set."
  [{:keys [open component on-click-outside]}]
  (if (= open true)
    [:div {:class "absolute top-full w-full z-20"}
     [:div {:class "fixed inset-0 bg-gray-50 w-full h-full z-10 opacity-30"
            :on-click on-click-outside}]
     [:div {:class "absolute z-10 min-w-36 bg-white border shadow top-full mt-2 -left-3 rounded-lg"}
      [:div#popover-container component]
      [:div {:class "-top-1.5 absolute w-3 h-3 bg-white border-t border-l rounded transform rotate-45 left-4"}]]]
    nil))
