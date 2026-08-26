import { useCallback } from "react";
import { useNavigate } from "react-router";
import { FeedEditor } from "@/components/FeedEditor";

// /feeds/new: the editor over an empty page (navigates to the new feed on save).
export default function FeedEditorPage() {
  const navigate = useNavigate();
  const onClose = useCallback(() => navigate(-1), [navigate]);
  return (
    <div className="p-10">
      <FeedEditor onClose={onClose} />
    </div>
  );
}
