(ns webapp.audit.views.empty-event-stream)

(defn main []
  [:div
   {:class (str "flex flex-col gap-regular justify-center items-center text-center"
                " h-96 py-large bg-gray-100 rounded-lg")}
   [:figure {:class "w-40"}
    [:img {:src "/images/illustrations/pc+monitor.svg"}]]
   [:div {:class "px-x-large"}
    [:p {:class "text-sm text-gray-600 font-bold"}
     (str "This session needs to be reviewed or executed.")]
    [:p {:class "text-xs text-gray-600 pt-small"}
     (str "Or maybe it's still running and you just need to wait it to end.")]]])

